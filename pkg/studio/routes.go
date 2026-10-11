// Package studio : l'API du studio de blocs — génération (simple ou en flux),
// catalogue, banque d'images, export de production et publication.
// La persistance des projets vit dans pkg/projects ; ici, on orchestre.
package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nadaprod/pkg/export"
	"nadaprod/pkg/gemini"
	"nadaprod/pkg/images"
	"nadaprod/pkg/projects"
	"nadaprod/pkg/prompts"
	"nadaprod/pkg/publish"
)

// Deps : tout ce dont les routes ont besoin, injecté par main.go (et par les
// tests, avec un faux générateur et des fournisseurs simulés).
type Deps struct {
	Gen        gemini.Generator
	Projects   *projects.Store
	Images     *images.Service
	Compile    export.Compiler // nil ⇒ export non compilé (repli CDN)
	FontsDir   string          // miroir local de polices ("" ⇒ aucune police de thème proposée)
	RatePerMin int             // générations par minute et par compte (≤ 0 ⇒ 30)

	// Passerelle avec les sites importés (nil ⇒ fonctions désactivées).
	Sites      SiteStore
	SiteAccess func(user, siteID string) bool // le compte peut-il toucher ce site ?

	// Surchargés en test uniquement.
	NetlifyURL    string
	CloudflareURL string
}

// SiteStore : ce que la passerelle attend de pkg/sites.
type SiteStore interface {
	ReadPage(id, rel string) ([]byte, error)
	SavePage(id, rel string, html []byte, overwrite bool) (string, error)
}

const (
	generateTimeout = 120 * time.Second
	exportTimeout   = 90 * time.Second
	publishTimeout  = 3 * time.Minute
)

// fail répond {error, code} — le code guide l'UI, jamais le message.
func fail(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"error": msg, "code": code})
}

// genError traduit un échec de génération en réponse {error, code, retry_after}.
func genError(err error) (int, gin.H) {
	var ge *gemini.Error
	if errors.As(err, &ge) {
		h := gin.H{"error": ge.Message, "code": ge.Code}
		if ge.RetryAfter > 0 {
			h["retry_after"] = ge.RetryAfter
		}
		return ge.HTTPStatus(), h
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, gin.H{"error": "la génération a dépassé le délai imparti", "code": "timeout"}
	}
	if errors.Is(err, context.Canceled) {
		return 499, gin.H{"error": "génération annulée", "code": "canceled"}
	}
	return http.StatusBadGateway, gin.H{"error": err.Error(), "code": "upstream"}
}

func Register(api *gin.RouterGroup, d Deps) {
	if d.Images == nil {
		d.Images = images.NewService(nil)
	}
	if d.RatePerMin <= 0 {
		d.RatePerMin = 30
	}
	limiter := newLimiter(d.RatePerMin, time.Minute)
	fonts := listFonts(d.FontsDir)

	// Catalogue des blocs (types, labels, presets) consommé par l'éditeur.
	api.GET("/blocks", func(c *gin.Context) {
		c.JSON(http.StatusOK, prompts.BlockCatalog())
	})

	// Ce que ce serveur sait faire : le studio adapte son interface.
	api.GET("/studio/config", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"model":     d.Gen.Model(),
			"images":    gin.H{"enabled": d.Images.Enabled(), "provider": d.Images.ProviderName()},
			"compile":   d.Compile != nil,
			"fonts":     fonts,
			"publish":   []string{"netlify", "cloudflare"},
			"rate":      d.RatePerMin,
			"user":      c.GetString("user"),
			"root":      c.GetBool("isRoot"),
			"max_block": projects.MaxBlocksPerProject,
		})
	})

	// prepare : validation commune aux deux modes de génération.
	prepare := func(c *gin.Context) (gemini.GenerateRequest, prompts.BlockSpec, bool) {
		var req gemini.GenerateRequest
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, "invalid", "requête invalide : "+err.Error())
			return req, prompts.BlockSpec{}, false
		}
		spec, ok := prompts.GetBlockSpec(req.Block)
		if !ok {
			fail(c, http.StatusBadRequest, "invalid", "type de bloc inconnu : "+req.Block)
			return req, spec, false
		}
		if wait := limiter.take(c.GetString("user")); wait > 0 {
			secs := int(wait.Seconds()) + 1
			c.Header("Retry-After", strconv.Itoa(secs))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "trop de générations à la suite — réessayez dans " + strconv.Itoa(secs) + " s",
				"code":  "rate_limited", "retry_after": secs,
			})
			return req, spec, false
		}
		return req, spec, true
	}

	// generate : génère le bloc (en flux si onDelta ≠ nil) puis remplace les
	// placeholders data-photo si une banque d'images est configurée.
	generate := func(ctx context.Context, spec prompts.BlockSpec, req gemini.GenerateRequest, onDelta func(string)) (gemini.GenerateResponse, error) {
		start := time.Now()
		var html string
		var err error
		if onDelta == nil {
			html, err = d.Gen.GenerateBlock(ctx, spec, req)
		} else {
			html, err = d.Gen.StreamBlock(ctx, spec, req, onDelta)
		}
		if err != nil {
			return gemini.GenerateResponse{}, err
		}
		n := 0
		if req.Images && d.Images.Enabled() {
			html, n = d.Images.Resolve(ctx, html)
		}
		return gemini.GenerateResponse{
			HTML: strings.TrimSpace(html), Block: req.Block, Model: d.Gen.Model(),
			ElapsedMs: time.Since(start).Milliseconds(), Photos: n,
		}, nil
	}

	// Génération d'un bloc via le générateur spécialisé.
	api.POST("/generate", func(c *gin.Context) {
		req, spec, ok := prepare(c)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), generateTimeout)
		defer cancel()
		res, err := generate(ctx, spec, req, nil)
		if err != nil {
			c.JSON(genError(err))
			return
		}
		c.JSON(http.StatusOK, res)
	})

	// Génération en flux (SSE) : « delta » au fil de l'eau, puis « done » avec
	// le HTML final nettoyé — ou « error ». Le studio affiche le bloc qui se
	// construit ; seul « done » fait foi.
	api.POST("/generate/stream", func(c *gin.Context) {
		req, spec, ok := prepare(c)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), generateTimeout)
		defer cancel()

		h := c.Writer.Header()
		h.Set("Content-Type", "text/event-stream; charset=utf-8")
		h.Set("Cache-Control", "no-cache, no-transform")
		h.Set("X-Accel-Buffering", "no") // nginx : ne pas tamponner le flux
		c.Status(http.StatusOK)
		send := func(event string, v any) {
			data, _ := json.Marshal(v)
			c.Writer.WriteString("event: " + event + "\ndata: " + string(data) + "\n\n")
			c.Writer.Flush()
		}

		res, err := generate(ctx, spec, req, func(delta string) { send("delta", gin.H{"text": delta}) })
		if err != nil {
			_, body := genError(err)
			send("error", body)
			return
		}
		send("done", res)
	})

	// Recherche dans la banque d'images (sélecteur manuel du studio).
	api.GET("/images/search", func(c *gin.Context) {
		if !d.Images.Enabled() {
			fail(c, http.StatusServiceUnavailable, "disabled", "aucune banque d'images configurée (PEXELS_API_KEY ou UNSPLASH_ACCESS_KEY)")
			return
		}
		q := strings.TrimSpace(c.Query("q"))
		if q == "" {
			fail(c, http.StatusBadRequest, "invalid", "recherche vide")
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		list, err := d.Images.Search(ctx, q, c.Query("orientation"), 18)
		if err != nil {
			fail(c, http.StatusBadGateway, "upstream", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"provider": d.Images.ProviderName(), "photos": list})
	})

	// page : le projet tel que l'export le comprend.
	page := func(c *gin.Context) (export.Page, projects.Project, bool) {
		var p export.Page
		if !d.Projects.Allowed(c, c.Param("id")) {
			return p, projects.Project{}, false
		}
		pr, err := d.Projects.Get(c.Request.Context(), c.Param("id"))
		if err != nil {
			fail(c, http.StatusInternalServerError, "internal", err.Error())
			return p, pr, false
		}
		p.Name = pr.Name
		// JSON stockés et déjà validés comme objets : un champ mal typé est
		// simplement ignoré (valeur zéro), l'export ne doit pas échouer pour ça.
		_ = json.Unmarshal(pr.Brand, &p.Brand)
		_ = json.Unmarshal(pr.Theme, &p.Theme)
		_ = json.Unmarshal(pr.SEO, &p.SEO)
		for _, b := range pr.Blocks {
			p.Blocks = append(p.Blocks, export.Block{ID: b.ID, Type: b.Type, HTML: b.HTML})
		}
		return p, pr, true
	}

	// siteAllowed : la passerelle est active et le compte peut toucher ce site.
	siteAllowed := func(c *gin.Context, site string) bool {
		if d.Sites == nil || d.SiteAccess == nil {
			fail(c, http.StatusServiceUnavailable, "disabled", "sites importés indisponibles")
			return false
		}
		if !d.SiteAccess(c.GetString("user"), site) {
			fail(c, http.StatusForbidden, "forbidden", "accès refusé à ce site")
			return false
		}
		return true
	}

	// remember mémorise la cible de publication avec le projet. Renvoie la
	// nouvelle révision, ou rev si l'écriture a échoué (la publication, elle, a réussi).
	remember := func(c *gin.Context, pid string, rev int64, target gin.H) int64 {
		meta, _ := json.Marshal(target)
		raw := json.RawMessage(meta)
		if out, err := d.Projects.UpdateMeta(c.Request.Context(), pid, projects.MetaInput{Publish: &raw}); err == nil {
			return out.Rev
		}
		return rev
	}

	// Export de production. format : html (fichier autonome), zip (site
	// complet), jsx / vue (site + components/).
	api.POST("/projects/:id/export", func(c *gin.Context) {
		var body struct {
			Format string `json:"format"`
		}
		_ = c.ShouldBindJSON(&body)
		p, _, ok := page(c)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), exportTimeout)
		defer cancel()
		slug := export.Slug(p.Name)

		var (
			res      export.Result
			data     []byte
			filename string
			mime     string
			err      error
		)
		switch body.Format {
		case "", "html":
			res = export.HTML(p, d.Compile)
			data, filename, mime = res.Files[0].Data, slug+".html", "text/html; charset=utf-8"
		case "zip", "jsx", "vue":
			comp := ""
			if body.Format != "zip" {
				comp = body.Format
			}
			res, err = export.Site(ctx, p, export.SiteOptions{Compiler: d.Compile, FontsDir: d.FontsDir, Components: comp})
			if err == nil {
				data, err = export.Zip(res.Files)
			}
			filename, mime = slug+".zip", "application/zip"
		default:
			fail(c, http.StatusBadRequest, "invalid", "format inconnu : "+body.Format)
			return
		}
		if err != nil {
			fail(c, http.StatusUnprocessableEntity, "export", err.Error())
			return
		}
		// Les avertissements voyagent en en-tête (JSON encodé URL) : le corps est le fichier.
		if len(res.Warnings) > 0 {
			w, _ := json.Marshal(res.Warnings)
			c.Header("X-Export-Warnings", strconv.QuoteToASCII(string(w)))
		}
		c.Header("X-Export-Compiled", strconv.FormatBool(res.Compiled))
		c.Header("Content-Disposition", "attachment; filename="+strconv.Quote(filename))
		c.Data(http.StatusOK, mime, data)
	})

	// Dépose la page du projet dans un site importé (dev/<path>) : la source
	// garde le CDN comme toute page importée, le pipeline compile ensuite.
	// La cible est mémorisée (publish.provider = "site") pour ré-enregistrer.
	api.POST("/projects/:id/site", func(c *gin.Context) {
		var body struct {
			Site      string `json:"site"`
			Path      string `json:"path"`
			Overwrite bool   `json:"overwrite"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Site == "" {
			fail(c, http.StatusBadRequest, "invalid", "site manquant")
			return
		}
		if !siteAllowed(c, body.Site) {
			return
		}
		p, pr, ok := page(c)
		if !ok {
			return
		}
		if !hasHTML(p) {
			fail(c, http.StatusUnprocessableEntity, "export", "la page ne contient aucun bloc généré")
			return
		}
		out, err := d.Sites.SavePage(body.Site, cmp.Or(body.Path, "index.html"), export.PageCDN(p), body.Overwrite)
		if err != nil {
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "existe déjà") {
				status = http.StatusConflict
			}
			fail(c, status, "site", err.Error())
			return
		}
		url := "/preview/" + body.Site + "/" + out
		rev := remember(c, pr.ID, pr.Rev, gin.H{"provider": "site", "site": body.Site, "path": out, "url": url, "deployed_at": time.Now().UTC().Format(time.RFC3339)})
		c.JSON(http.StatusOK, gin.H{"ok": true, "site": body.Site, "path": out, "url": url, "edit": "/sites#" + body.Site, "rev": rev})
	})

	// Ouvre une page existante d'un site dans le studio : chaque enfant du
	// <body> devient un bloc d'un nouveau projet.
	api.POST("/projects/from-page", func(c *gin.Context) {
		var body struct {
			Site string `json:"site"`
			Path string `json:"path"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Site == "" || body.Path == "" {
			fail(c, http.StatusBadRequest, "invalid", "site et page requis")
			return
		}
		if !siteAllowed(c, body.Site) {
			return
		}
		raw, err := d.Sites.ReadPage(body.Site, body.Path)
		if err != nil {
			fail(c, http.StatusNotFound, "not_found", err.Error())
			return
		}
		blocks, title, desc, lang := export.SplitPage(raw)
		if len(blocks) == 0 {
			fail(c, http.StatusUnprocessableEntity, "export", "aucun contenu exploitable dans cette page")
			return
		}
		seo, _ := json.Marshal(gin.H{"title": title, "description": desc, "lang": lang})
		in := projects.ProjectInput{Name: cmp.Or(title, body.Site+" — "+body.Path), SEO: seo}
		for _, b := range blocks {
			in.Blocks = append(in.Blocks, projects.BlockInput{Type: b.Type, HTML: b.HTML, Source: projects.SourceImport})
		}
		p, err := d.Projects.Create(c.Request.Context(), c.GetString("user"), in)
		if err != nil {
			fail(c, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		remember(c, p.ID, p.Rev, gin.H{"provider": "site", "site": body.Site, "path": body.Path, "url": "/preview/" + body.Site + "/" + body.Path})
		c.JSON(http.StatusCreated, gin.H{"id": p.ID, "blocks": len(p.Blocks)})
	})

	// Publication en un clic. Le jeton du fournisseur est fourni par
	// l'utilisateur à chaque appel : il n'est ni stocké, ni journalisé. Seule
	// la cible du dernier déploiement (fournisseur, site, URL) est mémorisée
	// avec le projet, pour redéployer au même endroit.
	api.POST("/projects/:id/publish", func(c *gin.Context) {
		var body struct {
			Provider  string `json:"provider"`
			Token     string `json:"token"`
			Site      string `json:"site"`       // Netlify : site existant à redéployer ; Cloudflare : nom du projet Pages
			AccountID string `json:"account_id"` // Cloudflare
		}
		err := c.ShouldBindJSON(&body)
		body.Token, body.Site, body.AccountID = strings.TrimSpace(body.Token), strings.TrimSpace(body.Site), strings.TrimSpace(body.AccountID)
		if err != nil || body.Token == "" {
			fail(c, http.StatusBadRequest, "invalid", "jeton du fournisseur manquant")
			return
		}
		p, pr, ok := page(c)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), publishTimeout)
		defer cancel()

		site, err := export.Site(ctx, p, export.SiteOptions{Compiler: d.Compile, FontsDir: d.FontsDir})
		if err != nil {
			fail(c, http.StatusUnprocessableEntity, "export", err.Error())
			return
		}
		files := make([]publish.File, 0, len(site.Files))
		for _, f := range site.Files {
			files = append(files, publish.File{Path: f.Path, Data: f.Data})
		}

		var res publish.Result
		switch body.Provider {
		case "netlify":
			n := &publish.Netlify{Token: body.Token, BaseURL: d.NetlifyURL}
			res, err = n.Deploy(ctx, body.Site, files)
		case "cloudflare":
			name := cmp.Or(body.Site, publish.SanitizeProjectName(p.Name))
			cf := &publish.Cloudflare{Token: body.Token, AccountID: body.AccountID, BaseURL: d.CloudflareURL}
			res, err = cf.Deploy(ctx, name, files)
		default:
			fail(c, http.StatusBadRequest, "invalid", "fournisseur inconnu : "+body.Provider)
			return
		}
		if err != nil {
			status, code := http.StatusBadGateway, "upstream"
			var pe *publish.Error
			switch {
			case errors.Is(err, publish.ErrInvalidInput):
				status, code = http.StatusBadRequest, "invalid"
			case errors.As(err, &pe):
				code = pe.Code
				if pe.Code == publish.CodeAuth {
					status = http.StatusUnprocessableEntity // pas 401 : ne pas confondre avec NOTRE 401 (session)
				}
			}
			h := gin.H{"error": err.Error(), "code": code}
			if res.Site != "" { // site créé mais déploiement en échec : à réutiliser
				h["site"] = res.Site
			}
			c.JSON(status, h)
			return
		}

		rev := remember(c, pr.ID, pr.Rev, gin.H{
			"provider": res.Provider, "site": res.Site, "url": res.URL, "account_id": body.AccountID,
			"deployed_at": time.Now().UTC().Format(time.RFC3339),
		})
		c.JSON(http.StatusOK, gin.H{"ok": true, "result": res, "warnings": site.Warnings, "compiled": site.Compiled, "rev": rev})
	})
}

// ---------- Polices du design system ----------

// Sélection resserrée : des familles sûres pour une page de présentation.
// Seules celles réellement présentes dans le miroir local sont proposées.
var curatedFonts = []struct{ Family, Kind string }{
	{"Inter", "sans"}, {"Manrope", "sans"}, {"DM Sans", "sans"}, {"Plus Jakarta Sans", "sans"}, {"Outfit", "sans"},
	{"Sora", "sans"}, {"Figtree", "sans"}, {"Urbanist", "sans"}, {"Work Sans", "sans"}, {"Poppins", "sans"},
	{"Montserrat", "sans"}, {"Nunito", "sans"}, {"Rubik", "sans"}, {"Lexend", "sans"}, {"IBM Plex Sans", "sans"},
	{"Space Grotesk", "display"}, {"Bricolage Grotesque", "display"}, {"Syne", "display"}, {"Archivo", "display"},
	{"Unbounded", "display"}, {"Oswald", "display"}, {"Bebas Neue", "display"},
	{"Playfair Display", "serif"}, {"Fraunces", "serif"}, {"DM Serif Display", "serif"}, {"Instrument Serif", "serif"},
	{"Cormorant Garamond", "serif"}, {"Lora", "serif"}, {"Merriweather", "serif"}, {"Libre Baskerville", "serif"},
	{"Source Serif 4", "serif"}, {"IBM Plex Serif", "serif"},
	{"JetBrains Mono", "mono"}, {"Space Mono", "mono"},
}

// Font : une famille disponible et les graisses que le miroir sait servir.
type Font struct {
	Family  string `json:"family"`
	Kind    string `json:"kind"`
	Weights []int  `json:"weights"`
}

func listFonts(dir string) []Font {
	out := []Font{}
	if dir == "" {
		return out
	}
	for _, f := range curatedFonts {
		var ws []int
		for _, v := range []struct {
			file string
			w    int
		}{{"regular.ttf", 400}, {"500.ttf", 500}, {"600.ttf", 600}, {"700.ttf", 700}} {
			if _, err := os.Stat(filepath.Join(dir, f.Family, v.file)); err == nil {
				ws = append(ws, v.w)
			}
		}
		if len(ws) > 0 {
			out = append(out, Font{f.Family, f.Kind, ws})
		}
	}
	return out
}

func hasHTML(p export.Page) bool {
	for _, b := range p.Blocks {
		if strings.TrimSpace(b.HTML) != "" {
			return true
		}
	}
	return false
}
