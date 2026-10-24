package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"nadaprod/pkg/auth"
	"nadaprod/pkg/gemini"
	"nadaprod/pkg/images"
	"nadaprod/pkg/projects"
	"nadaprod/pkg/stats"
	"nadaprod/pkg/studio"
)

// envOr : variable texte, valeur par défaut si absente ou vide.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envFloat / envInt : variable numérique optionnelle (absente ou illisible ⇒ 0,
// le destinataire applique alors sa valeur par défaut).
func envFloat(key string) float64 {
	v, _ := strconv.ParseFloat(os.Getenv(key), 64)
	return v
}

func envInt(key string) int {
	v, _ := strconv.Atoi(os.Getenv(key))
	return v
}

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

	// « nadaprod build-site <id>… » : republie des sites puis rend la main
	// (bin/build.sh de chaque site). Avant tout le reste : ni clé Gemini ni
	// compte root ne sont nécessaires pour ça.
	if len(os.Args) > 1 && os.Args[1] == "build-site" {
		os.Exit(buildSites(os.Args[2:]))
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY manquante. Exportez votre clé : export GEMINI_API_KEY=...")
	}

	// Alias officiel Google pointant toujours vers le dernier Flash stable.
	model := envOr("GEMINI_MODEL", "gemini-3.7-flash")

	// Mesure de /debate (doc/STATS.md) : appels IA et visites des sites
	// publiés, agrégés par jour — aucune donnée personnelle.
	statsStore, err := stats.Open(envOr("STATS_FILE", "./stats.db"))
	if err != nil {
		log.Fatal("initialisation de la base stats : ", err)
	}

	client := gemini.NewClient(apiKey, model, gemini.Options{
		Temperature:     envFloat("GEMINI_TEMPERATURE"), // défaut 0.7
		MaxOutputTokens: envInt("GEMINI_MAX_TOKENS"),    // défaut 8192
		OnUsage: func(e gemini.UsageEvent) {
			statsStore.AI(stats.AIEvent{Kind: e.Kind, Model: e.Model, OK: e.OK,
				PromptTokens: e.PromptTokens, OutputTokens: e.OutputTokens, Duration: e.Duration})
		},
	})

	store, err := siteStore()
	if err != nil {
		log.Fatal("initialisation du stockage sites : ", err)
	}
	go store.BuildAll() // publie tous les sites en arrière-plan au démarrage
	store.OnHit = func(site string, status int, bytes int64, dur time.Duration, html bool, ua string) {
		statsStore.Hit(site, status, bytes, dur, html, stats.IsBot(ua))
	}

	// Comptes : root vient de l'environnement, les autres de users.json.
	rootEmail := envOr("ROOT_EMAIL", "web@l3dlp.com")
	rootPassword := os.Getenv("ROOT_PASSWORD")
	if rootPassword == "" {
		log.Fatal("ROOT_PASSWORD manquant : l'application est protégée par authentification, définissez-le dans .env")
	}
	users, err := auth.NewUserStore(envOr("USERS_FILE", "./users.json"), envOr("WAITLIST_FILE", "./waiting-list.json"),
		rootEmail, rootPassword, os.Getenv("AUTH_SECRET"))
	if err != nil {
		log.Fatal("initialisation des comptes : ", err)
	}

	// Projets du studio de blocs (SQLite, un seul fichier).
	projectStore, err := projects.Open(envOr("DB_FILE", "./nadaprod.db"))
	if err != nil {
		log.Fatal("initialisation de la base projets : ", err)
	}
	defer projectStore.Close()

	// Banque d'images (optionnelle) : PEXELS_API_KEY, sinon UNSPLASH_ACCESS_KEY.
	photos := images.FromEnv(os.Getenv)
	if photos.Enabled() {
		log.Printf("banque d'images : %s", photos.ProviderName())
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
	for _, page := range []string{"demo", "login", "waiting-list", "debate", // /<page> → ./web/<page>.html
		"about", "legal", "opensource", "privacy", "solutions", "terms"} {
		r.StaticFile("/"+page, "./web/"+page+".html")
	}

	r.Static("/gfx", "./web/gfx")
	r.Static("/static", "./web/static")
	// Le miroir de polices est public par nature ; l'aperçu du studio tourne
	// dans une iframe isolée (origine opaque) et a besoin de CORS pour les charger.
	r.Group("/webfonts", func(c *gin.Context) { c.Header("Access-Control-Allow-Origin", "*") }).
		Static("/", "./web/webfonts")
	r.MaxMultipartMemory = 32 << 20

	// L'app elle-même (studio, sites, édition) exige une session valide.
	authed := r.Group("/", users.RequireAuth())
	authed.StaticFile("/editor", "./web/studio.html") // studio de blocs (l'app)
	authed.GET("/editor/:project", func(c *gin.Context) { c.File("./web/studio.html") })
	authed.StaticFile("/sites", "./web/sites.html")
	authed.StaticFile("/drafts", "./web/drafts.html")

	api := r.Group("/api")                          // login, health
	apiAuthed := api.Group("", users.RequireAuth()) // tout le reste
	users.RegisterAuthRoutes(r, api, apiAuthed)
	store.RegisterRoutes(authed, apiAuthed, users)
	projectStore.RegisterRoutes(apiAuthed)

	var compile func([]byte, string) ([]byte, error)
	if store.CanCompile() {
		compile = store.CompileCSS
	}
	studio.Register(apiAuthed, studio.Deps{
		Gen:        client,
		Translator: client,
		Projects:   projectStore,
		Images:     photos,
		Compile:    compile,
		FontsDir:   store.FontsDir(),
		RatePerMin: envInt("GENERATE_RATE_PER_MIN"), // défaut 30
		Sites:      store,
		SiteAccess: users.CanAccess,
	})

	// Chiffres publics de /debate (cache 60 s) ; détail par site pour root.
	stats.Register(api, apiAuthed.Group("", auth.RequireRoot()), stats.Deps{
		Store: statsStore,
		Counts: func(ctx context.Context) (stats.AppCounts, error) {
			pc, err := projectStore.Counts(ctx)
			if err != nil {
				return stats.AppCounts{}, err
			}
			sites, live, pages, err := store.Counts()
			return stats.AppCounts{Projects: pc.Projects, Blocks: pc.Blocks, Versions: pc.Versions, Translations: pc.Translations,
				Sites: sites, LiveSites: live, Pages: pages}, err
		},
	})

	api.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "model": model})
	})

	r.NoRoute(func(c *gin.Context) {
		// c.File passerait par http.ServeFile qui force un statut 200.
		page, err := os.ReadFile("./web/404.html")
		if err != nil {
			c.Redirect(http.StatusMovedPermanently, "/")
			return
		}
		c.Data(http.StatusNotFound, "text/html; charset=utf-8", page)
	})

	// Arrêt du service : le tampon des visites est écrit avant de partir.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		_ = statsStore.Close()
		_ = projectStore.Close()
		os.Exit(0)
	}()

	addr := envOr("ADDR", ":8080")
	log.Printf("NADAPROD sur http://localhost%s (modèle %s)", addr, model)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}
