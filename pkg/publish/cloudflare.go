package publish

// ATTENTION — FLUX NON DOCUMENTÉ, JAMAIS TESTÉ CONTRE L'API RÉELLE.
//
// Les points d'entrée d'envoi direct utilisés ici (…/upload-token et
// /pages/assets/check-missing, /pages/assets/upload, /pages/assets/upsert-hashes)
// ne font PAS partie de l'API publique documentée de Cloudflare. Ce fichier
// reproduit le comportement de `wrangler pages deploy` (Direct Upload) tel
// qu'il a été spécifié — formats des requêtes, calcul des empreintes, jeton JWT
// sur les routes d'assets — et n'a été vérifié que contre un serveur simulé
// (net/http/httptest), jamais contre api.cloudflare.com. Les formats de
// réponse supposés viennent de la même spécification, pas d'une observation.
// À valider avec un vrai compte avant d'exposer la fonction à des
// utilisateurs ; Cloudflare peut modifier ce flux sans préavis.
//
// Seules la lecture/création de projet et la création de déploiement
// appartiennent à l'API documentée.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"lukechampine.com/blake3"
)

const (
	cloudflareDefaultBaseURL = "https://api.cloudflare.com/client/v4"
	cloudflareBranch         = "main"
	fallbackProjectName      = "nadaprod-site"
	maxProjectNameLen        = 58
)

// Taille des lots d'envoi : bornes de la spécification (wrangler raisonne de
// même, en nombre de fichiers et en volume base64). Variables pour les tests.
var (
	cloudflareBatchFiles = 50
	cloudflareBatchBytes = 40 << 20
)

// Cloudflare : déploiement Pages par envoi direct (voir l'avertissement en
// tête de fichier).
type Cloudflare struct {
	Token     string
	AccountID string
	BaseURL   string // défaut "https://api.cloudflare.com/client/v4"
	HTTP      *http.Client
}

type cloudflareEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		// Entier en pratique. RawMessage : un autre type ne doit pas faire
		// rejeter toute l'enveloppe, et un grand entier ne doit pas passer par
		// float64 (8000007 deviendrait « 8.000007e+06 »).
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

type cloudflareProject struct {
	Name             string `json:"name"`
	Subdomain        string `json:"subdomain"`
	ProductionBranch string `json:"production_branch"`
}

type cloudflareAsset struct {
	file        File
	hash        string
	contentType string
}

type cloudflareUploadItem struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Metadata struct {
		ContentType string `json:"contentType"`
	} `json:"metadata"`
	Base64 bool `json:"base64"`
}

var (
	projectNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,56}[a-z0-9])?$`)
	accountIDRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// Deploy : project est le nom du projet Pages (créé s'il n'existe pas,
// production_branch "main").
//
// Le déploiement vise la branche de production du projet : "main" pour un
// projet créé ici, mais celle déclarée par le projet s'il existait déjà avec
// une autre — sinon Cloudflare en ferait un déploiement d'aperçu et l'URL
// publique renvoyée ne montrerait pas le nouveau contenu.
func (c *Cloudflare) Deploy(ctx context.Context, project string, files []File) (Result, error) {
	if err := ValidateFiles(files); err != nil {
		return Result{}, err
	}
	token, err := cleanToken(c.Token, "Cloudflare")
	if err != nil {
		return Result{}, err
	}
	account := strings.TrimSpace(c.AccountID)
	if !accountIDRe.MatchString(account) {
		return Result{}, invalidf("identifiant de compte Cloudflare invalide")
	}
	if !projectNameRe.MatchString(project) {
		return Result{}, invalidf("nom de projet Cloudflare invalide : %q (minuscules, chiffres et tirets, 1 à %d caractères)", project, maxProjectNameLen)
	}
	projectPath := "/accounts/" + url.PathEscape(account) + "/pages/projects/" + url.PathEscape(project)

	// 1. Projet : lecture, création sur 404.
	var proj cloudflareProject
	err = c.call(ctx, token, http.MethodGet, projectPath, "", nil, &proj)
	var pe *Error
	if errors.As(err, &pe) && pe.Status == http.StatusNotFound {
		body, _ := json.Marshal(map[string]string{"name": project, "production_branch": cloudflareBranch})
		proj = cloudflareProject{}
		err = c.call(ctx, token, http.MethodPost, "/accounts/"+url.PathEscape(account)+"/pages/projects", "application/json", body, &proj)
	}
	if err != nil {
		return Result{}, err
	}

	// 2. Jeton d'envoi : wrangler s'authentifie sur /pages/assets/* avec ce JWT
	// de courte durée, pas avec le jeton API.
	var tok struct {
		JWT string `json:"jwt"`
	}
	if err := c.call(ctx, token, http.MethodGet, projectPath+"/upload-token", "", nil, &tok); err != nil {
		return Result{}, err
	}
	if tok.JWT == "" {
		return Result{}, upstreamError("cloudflare", http.StatusOK, "réponse inattendue : jeton d'envoi absent")
	}

	// 3. Empreintes. Deux fichiers identiques (même contenu, même extension)
	// partagent une empreinte : on ne la déclare et ne l'envoie qu'une fois.
	assets := buildAssets(files)
	manifest := make(map[string]string, len(assets))
	var hashes []string
	byHash := make(map[string]cloudflareAsset, len(assets))
	for _, a := range assets {
		manifest["/"+a.file.Path] = a.hash
		if _, dup := byHash[a.hash]; !dup {
			byHash[a.hash] = a
			hashes = append(hashes, a.hash)
		}
	}
	hashesBody, _ := json.Marshal(map[string][]string{"hashes": hashes})

	// 4. Cloudflare indique ce qu'il ne possède pas encore.
	var missing []string
	if err := c.call(ctx, tok.JWT, http.MethodPost, "/pages/assets/check-missing", "application/json", hashesBody, &missing); err != nil {
		return Result{}, err
	}
	var toUpload []cloudflareAsset
	wanted := make(map[string]bool, len(missing))
	for _, h := range missing {
		wanted[h] = true
	}
	for _, h := range hashes { // ordre des fichiers, pas celui de la réponse : envois reproductibles
		if wanted[h] {
			toUpload = append(toUpload, byHash[h])
		}
	}

	// 5. Envoi par lots. Le base64 n'est produit que lot par lot pour ne pas
	// tenir tout le site encodé en mémoire en plus de l'original.
	for _, batch := range splitBatches(toUpload, cloudflareBatchFiles, cloudflareBatchBytes) {
		items := make([]cloudflareUploadItem, len(batch))
		for i, a := range batch {
			items[i].Key = a.hash
			items[i].Value = base64.StdEncoding.EncodeToString(a.file.Data)
			items[i].Metadata.ContentType = a.contentType
			items[i].Base64 = true
		}
		body, _ := json.Marshal(items)
		if err := c.call(ctx, tok.JWT, http.MethodPost, "/pages/assets/upload", "application/json", body, nil); err != nil {
			return Result{}, err
		}
	}

	// 6. Déclare toutes les empreintes du déploiement, déjà présentes ou non,
	// comme le fait wrangler. Échec bloquant ici (wrangler, lui, se contente
	// d'un avertissement) : mieux vaut un refus net qu'un état incertain.
	if err := c.call(ctx, tok.JWT, http.MethodPost, "/pages/assets/upsert-hashes", "application/json", hashesBody, nil); err != nil {
		return Result{}, err
	}

	// 7. Déploiement : manifeste chemin → empreinte, en multipart.
	branch := cmp.Or(proj.ProductionBranch, cloudflareBranch)
	manifestJSON, _ := json.Marshal(manifest)
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	_ = mw.WriteField("manifest", string(manifestJSON))
	_ = mw.WriteField("branch", branch)
	if err := mw.Close(); err != nil {
		return Result{}, err
	}
	var dep struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := c.call(ctx, token, http.MethodPost, projectPath+"/deployments", mw.FormDataContentType(), form.Bytes(), &dep); err != nil {
		return Result{}, err
	}

	res := Result{
		Provider: "cloudflare",
		URL:      dep.URL, // URL propre à ce déploiement, faute de mieux
		DeployID: dep.ID,
		Site:     project,
		// Forme d'URL du tableau de bord reprise de wrangler ; non vérifiée en réel.
		AdminURL: "https://dash.cloudflare.com/" + account + "/pages/view/" + project,
	}
	// Le sous-domaine du projet est l'adresse stable ; il peut différer du nom
	// du projet quand celui-ci est déjà pris sur pages.dev.
	if sub := strings.TrimSpace(proj.Subdomain); sub != "" {
		if !strings.Contains(sub, "://") {
			sub = "https://" + sub
		}
		res.URL = sub
	}
	return res, nil
}

// call : requête + dépouillement de l'enveloppe {success, errors, result}.
// bearer est le jeton API ou le JWT d'envoi selon la route.
func (c *Cloudflare) call(ctx context.Context, bearer, method, p, contentType string, body []byte, out any) error {
	status, data, err := send(ctx, c.HTTP, "cloudflare", method, endpoint(c.BaseURL, cloudflareDefaultBaseURL, p), bearer, contentType, body)
	if err != nil {
		return err
	}
	var env cloudflareEnvelope
	parseErr := json.Unmarshal(data, &env)
	if status < 200 || status > 299 || (parseErr == nil && !env.Success) {
		msg := snippet(status, data)
		if parseErr == nil && len(env.Errors) > 0 && env.Errors[0].Message != "" {
			msg = env.Errors[0].Message
			if code := strings.Trim(string(env.Errors[0].Code), `"`); code != "" && code != "null" {
				msg = fmt.Sprintf("%s (code %s)", msg, code)
			}
		}
		// success:false sous un statut 2xx : anomalie côté fournisseur ⇒ "upstream".
		return &Error{Provider: "cloudflare", Status: status, Code: codeForStatus(status), Message: msg}
	}
	if parseErr == nil && (out == nil || len(env.Result) == 0 || string(env.Result) == "null") {
		return nil
	}
	if parseErr != nil || json.Unmarshal(env.Result, out) != nil {
		return upstreamError("cloudflare", status, "réponse illisible")
	}
	return nil
}

// buildAssets : triés par chemin pour que l'ordre des empreintes et des lots
// ne dépende pas de l'ordre d'arrivée des fichiers.
func buildAssets(files []File) []cloudflareAsset {
	assets := make([]cloudflareAsset, len(files))
	for i, f := range files {
		assets[i] = cloudflareAsset{file: f, hash: assetHash(f), contentType: contentTypeFor(f.Path)}
	}
	slices.SortFunc(assets, func(a, b cloudflareAsset) int { return byPath(a.file, b.file) })
	return assets
}

// assetHash : empreinte d'asset identique à celle de wrangler —
// hex(blake3_256(base64std(contenu) + extensionSansPoint))[:32]. L'ordre de
// concaténation et la troncature font partie du contrat : les modifier ferait
// renvoyer tout le site à chaque publication.
func assetHash(f File) string {
	sum := blake3.Sum256([]byte(base64.StdEncoding.EncodeToString(f.Data) + assetExt(f.Path)))
	return hex.EncodeToString(sum[:])[:32]
}

// assetExt : extension sans le point, avec la sémantique de path.extname de
// Node (celle de wrangler) : un fichier caché sans autre point (« .htaccess »)
// n'a pas d'extension, là où path.Ext de Go renverrait le nom entier.
func assetExt(p string) string {
	base := path.Base(p)
	ext := path.Ext(base)
	if ext == base {
		return ""
	}
	return strings.TrimPrefix(ext, ".")
}

// webTypes : types servis tels quels par Pages. Table locale d'abord, car
// mime.TypeByExtension dépend des fichiers mime.types de la machine hôte.
var webTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".htm":         "text/html; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".xml":         "application/xml",
	".txt":         "text/plain; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".ico":         "image/x-icon",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".pdf":         "application/pdf",
	".mp4":         "video/mp4",
	".webm":        "video/webm",
	".mp3":         "audio/mpeg",
	".wasm":        "application/wasm",
}

func contentTypeFor(p string) string {
	ext := strings.ToLower(path.Ext(p))
	if t, ok := webTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

// splitBatches : lots de maxFiles fichiers et maxBytes octets de base64 au
// plus. Un fichier qui dépasse maxBytes à lui seul part dans son propre lot
// (impossible en pratique : ValidateFiles plafonne le site à 25 Mo).
func splitBatches(assets []cloudflareAsset, maxFiles, maxBytes int) [][]cloudflareAsset {
	var batches [][]cloudflareAsset
	var cur []cloudflareAsset
	size := 0
	for _, a := range assets {
		n := base64.StdEncoding.EncodedLen(len(a.file.Data))
		if len(cur) > 0 && (len(cur) >= maxFiles || size+n > maxBytes) {
			batches = append(batches, cur)
			cur, size = nil, 0
		}
		cur = append(cur, a)
		size += n
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	return batches
}

// accentFolder : translittération volontairement simple des lettres
// accentuées courantes (français et voisins) ; le reste devient un tiret.
var accentFolder = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "á", "a", "ã", "a", "å", "a",
	"ç", "c",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "í", "i", "ì", "i",
	"ô", "o", "ö", "o", "ó", "o", "ò", "o", "õ", "o", "ø", "o",
	"ù", "u", "û", "u", "ü", "u", "ú", "u",
	"ÿ", "y", "ý", "y", "ñ", "n",
	"œ", "oe", "æ", "ae", "ß", "ss",
)

var nonSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// SanitizeProjectName : transforme un nom de marque quelconque (« Nébula
// Café ! ») en nom de projet Pages valide (« nebula-cafe ») : minuscules,
// chiffres et tirets, 58 caractères au plus, sans tiret aux extrémités.
// Rien d'exploitable ⇒ "nadaprod-site".
func SanitizeProjectName(s string) string {
	s = strings.ToLower(s)
	// Forme décomposée (e + accent combinant) : on retire la marque pour
	// retomber sur la lettre nue au lieu de couper le mot par un tiret.
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return r
	}, s)
	s = accentFolder.Replace(s)
	s = strings.Trim(nonSlugRe.ReplaceAllString(s, "-"), "-")
	if len(s) > maxProjectNameLen {
		s = strings.TrimRight(s[:maxProjectNameLen], "-")
	}
	if s == "" {
		return fallbackProjectName
	}
	return s
}
