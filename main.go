package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

// ---------- Types API ----------

type Brand struct {
	Name   string `json:"name"`
	Colors string `json:"colors"` // ex: "fond sombre #0B0D12, accent violet #8B5CF6"
	Tone   string `json:"tone"`   // ex: "startup SaaS, moderne, direct"
}

type GenerateRequest struct {
	Block       string `json:"block" binding:"required"` // header, hero, features...
	Prompt      string `json:"prompt"`
	Brand       Brand  `json:"brand"`
	CurrentHTML string `json:"current_html"` // pour itérer sur un bloc existant
}

type GenerateResponse struct {
	HTML      string `json:"html"`
	Block     string `json:"block"`
	Model     string `json:"model"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

// ---------- main ----------

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
		model = "gemini-flash-latest"
	}

	client := NewGeminiClient(apiKey, model)

	sitesRoot := os.Getenv("SITES_DIR")
	if sitesRoot == "" {
		sitesRoot = "./sites"
	}
	store, err := NewSiteStore(sitesRoot)
	if err != nil {
		log.Fatal("initialisation du stockage sites : ", err)
	}

	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// Point 3 — routage par domaine : si le Host correspond à un site
	// paramétré, on le sert directement et on court-circuite l'éditeur.
	r.Use(store.HostMiddleware())

	// Front statique
	r.StaticFile("/", "./web/home.html")          // page vitrine publique NADAPROD
	r.StaticFile("/robots.txt", "./web/robots.txt")
	r.StaticFile("/editor", "./web/index.html")   // studio de blocs (l'app)
	r.StaticFile("/sites", "./web/sites.html")

	r.StaticFile("/about", "./web/about.html")
	r.StaticFile("/legal", "./web/legal.html")
	r.StaticFile("/opensource", "./web/opensource.html")
	r.StaticFile("/privacy", "./web/privacy.html")
	r.StaticFile("/solutions", "./web/solutions.html")
	r.StaticFile("/terms", "./web/terms.html")

	r.Static("/gfx", "./web/gfx")
	r.Static("/static", "./web/static")
	r.MaxMultipartMemory = 32 << 20

	api := r.Group("/api")
	store.RegisterRoutes(r, api)
	{
		api.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"ok": true, "model": model})
		})

		// Catalogue des blocs (types, labels, presets) consommé par l'éditeur.
		api.GET("/blocks", func(c *gin.Context) {
			c.JSON(http.StatusOK, BlockCatalog())
		})

		// Génération d'un bloc via le générateur spécialisé.
		api.POST("/generate", func(c *gin.Context) {
			var req GenerateRequest
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "requête invalide : " + err.Error()})
				return
			}

			spec, ok := GetBlockSpec(req.Block)
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

			c.JSON(http.StatusOK, GenerateResponse{
				HTML:      strings.TrimSpace(html),
				Block:     req.Block,
				Model:     model,
				ElapsedMs: time.Since(start).Milliseconds(),
			})
		})
	}

	r.NoRoute(func(c *gin.Context) {
		c.File("./web/404.html") 
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
