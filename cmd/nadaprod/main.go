package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"nadaprod/pkg/auth"
	"nadaprod/pkg/gemini"
	"nadaprod/pkg/prompts"
	"nadaprod/pkg/sites"
)

// main — le câblage : config (.env), construction des stores, chaîne de
// middlewares (domaine → statiques publics → auth) et API du studio de blocs.
// Le binaire s'exécute depuis la racine du dépôt (chemins ./web, ./sites…).
func main() {
	// Charge un fichier .env s'il existe (les variables déjà présentes dans
	// l'environnement ont la priorité, godotenv n'écrase rien). Son absence
	// n'est pas une erreur : en prod, on peut tout passer par l'environnement.
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("lecture du .env ignorée : %v", err)
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY manquante. Exportez votre clé : export GEMINI_API_KEY=...")
	}

	model := os.Getenv("GEMINI_MODEL")
	if model == "" {
		// Alias officiel Google pointant toujours vers le dernier Flash stable.
		model = "gemini-3.7-flash"
	}

	client := gemini.NewClient(apiKey, model)

	sitesRoot := os.Getenv("SITES_DIR")
	if sitesRoot == "" {
		sitesRoot = "./sites"
	}

	// Binaire standalone Tailwind (compilation locale des sites publiés).
	// Absent ⇒ dégradation douce : les pages publiées gardent le CDN.
	twBin := os.Getenv("TAILWIND_BIN")
	if twBin == "" {
		twBin = "./bin/tailwindcss"
	}
	if _, err := os.Stat(twBin); err != nil {
		log.Printf("tailwindcss introuvable (%s) — compilation locale désactivée, CDN conservé", twBin)
		twBin = ""
	}

	// Miroir local Google Fonts (servi sur /webfonts, réutilisé par le build).
	// Absent ⇒ dégradation douce : les sites publiés gardent leurs liens Google.
	fontsDir := os.Getenv("WEBFONTS_DIR")
	if fontsDir == "" {
		fontsDir = "./web/webfonts"
	}
	if st, err := os.Stat(fontsDir); err != nil || !st.IsDir() {
		log.Printf("webfonts introuvable (%s) — polices locales désactivées sur les sites publiés", fontsDir)
		fontsDir = ""
	}

	// Les traductions du bandeau RGPD injecté viennent des mêmes JSON que l'UI.
	store, err := sites.NewStore(sitesRoot, twBin, fontsDir, "./web/static/i18n")
	if err != nil {
		log.Fatal("initialisation du stockage sites : ", err)
	}
	go store.BuildAll() // publie tous les sites en arrière-plan au démarrage

	// Comptes : root vient de l'environnement, les autres de users.json.
	rootEmail := os.Getenv("ROOT_EMAIL")
	if rootEmail == "" {
		rootEmail = "web@l3dlp.com"
	}
	rootPassword := os.Getenv("ROOT_PASSWORD")
	if rootPassword == "" {
		log.Fatal("ROOT_PASSWORD manquant : l'application est protégée par authentification, définissez-le dans .env")
	}
	usersFile := os.Getenv("USERS_FILE")
	if usersFile == "" {
		usersFile = "./users.json"
	}
	waitlistFile := os.Getenv("WAITLIST_FILE")
	if waitlistFile == "" {
		waitlistFile = "./waiting-list.json"
	}
	users, err := auth.NewUserStore(usersFile, waitlistFile, rootEmail, rootPassword, os.Getenv("AUTH_SECRET"))
	if err != nil {
		log.Fatal("initialisation des comptes : ", err)
	}

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// Routage par domaine : si le Host correspond à un site paramétré, on le
	// sert directement et on court-circuite l'éditeur (et l'authentification).
	r.Use(store.HostMiddleware())

	// Front statique public (vitrine, connexion, liste d'attente)
	r.StaticFile("/", "./web/home.html") // page vitrine publique NADAPROD
	r.StaticFile("/robots.txt", "./web/robots.txt")
	r.StaticFile("/demo", "./web/demo.html")
	r.StaticFile("/login", "./web/login.html")
	r.StaticFile("/waiting-list", "./web/waiting-list.html")

	r.StaticFile("/about", "./web/about.html")
	r.StaticFile("/legal", "./web/legal.html")
	r.StaticFile("/opensource", "./web/opensource.html")
	r.StaticFile("/privacy", "./web/privacy.html")
	r.StaticFile("/solutions", "./web/solutions.html")
	r.StaticFile("/terms", "./web/terms.html")

	r.Static("/gfx", "./web/gfx")
	r.Static("/static", "./web/static")
	r.Static("/webfonts", "./web/webfonts")
	r.MaxMultipartMemory = 32 << 20

	// L'app elle-même (studio, sites, édition) exige une session valide.
	authed := r.Group("/", users.RequireAuth())
	authed.StaticFile("/editor", "./web/index.html") // studio de blocs (l'app)
	authed.StaticFile("/sites", "./web/sites.html")
	authed.StaticFile("/drafts", "./web/drafts.html")

	api := r.Group("/api")                          // login, health
	apiAuthed := api.Group("", users.RequireAuth()) // tout le reste
	users.RegisterAuthRoutes(r, api, apiAuthed)
	store.RegisterRoutes(authed, apiAuthed, users)
	{
		api.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true, "model": model})
		})

		// Catalogue des blocs (types, labels, presets) consommé par l'éditeur.
		apiAuthed.GET("/blocks", func(c *gin.Context) {
			c.JSON(http.StatusOK, prompts.BlockCatalog())
		})

		// Génération d'un bloc via le générateur spécialisé.
		apiAuthed.POST("/generate", func(c *gin.Context) {
			var req gemini.GenerateRequest
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "requête invalide : " + err.Error()})
				return
			}

			spec, ok := prompts.GetBlockSpec(req.Block)
			if !ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": "type de bloc inconnu : " + req.Block})
				return
			}

			start := time.Now()
			html, err := client.GenerateBlock(c.Request.Context(), spec, req)
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
				return
			}

			c.JSON(http.StatusOK, gemini.GenerateResponse{
				HTML:      strings.TrimSpace(html),
				Block:     req.Block,
				Model:     model,
				ElapsedMs: time.Since(start).Milliseconds(),
			})
		})
	}

	r.NoRoute(func(c *gin.Context) {
		// c.File passerait par http.ServeFile qui force un statut 200.
		page, err := os.ReadFile("./web/404.html")
		if err != nil {
			c.Redirect(301, "/")
			return
		}
		c.Data(http.StatusNotFound, "text/html; charset=utf-8", page)
	})

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("NADAPROD sur http://localhost%s (modèle %s)", addr, model)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}
