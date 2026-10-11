package publish

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"lukechampine.com/blake3"
)

const (
	cfToken   = "cf-api-token-secret"
	cfJWT     = "jwt-abc"
	cfAccount = "acc123"
	cfProject = "nebula-cafe"

	cfProjectPath = "/accounts/acc123/pages/projects/nebula-cafe"
)

func cfOK(w http.ResponseWriter, result string) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"success":true,"errors":[],"messages":[],"result":%s}`, result)
}

func cfFail(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"success":false,"errors":[{"code":%d,"message":%q}],"messages":[],"result":null}`, code, msg)
}

// fakeCloudflare : le flux complet en mémoire. override force la réponse
// d'une route ("METHOD /chemin") pour les scénarios d'échec.
type fakeCloudflare struct {
	rec      recorder
	exists   bool                           // le projet existe déjà
	project  string                         // objet projet renvoyé (GET ou POST)
	missing  func(hashes []string) []string // réponse de check-missing
	override map[string]func(w http.ResponseWriter)
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req := f.rec.add(r)
	if o, ok := f.override[req.key()]; ok {
		o(w)
		return
	}
	switch req.key() {
	case "GET " + cfProjectPath:
		if !f.exists {
			cfFail(w, http.StatusNotFound, 8000007, "Project not found. The specified project name does not match any of your existing projects.")
			return
		}
		cfOK(w, f.project)
	case "POST /accounts/acc123/pages/projects":
		cfOK(w, f.project)
	case "GET " + cfProjectPath + "/upload-token":
		cfOK(w, `{"jwt":"`+cfJWT+`"}`)
	case "POST /pages/assets/check-missing":
		var in struct {
			Hashes []string `json:"hashes"`
		}
		json.Unmarshal(req.Body, &in)
		out, _ := json.Marshal(append([]string{}, f.missing(in.Hashes)...))
		cfOK(w, string(out))
	case "POST /pages/assets/upload", "POST /pages/assets/upsert-hashes":
		cfOK(w, "null")
	case "POST " + cfProjectPath + "/deployments":
		cfOK(w, `{"id":"dep-cf-1","url":"https://a1b2c3d4.nebula-cafe.pages.dev"}`)
	default:
		w.WriteHeader(http.StatusTeapot)
	}
}

func newFakeCloudflare(t *testing.T, f *fakeCloudflare) (*fakeCloudflare, *Cloudflare) {
	t.Helper()
	if f.missing == nil {
		f.missing = func([]string) []string { return nil }
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, &Cloudflare{Token: cfToken, AccountID: cfAccount, BaseURL: srv.URL}
}

// formFields : champs d'un corps multipart, dans l'ordre.
func formFields(t *testing.T, r recorded) (names []string, values map[string]string) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(r.ContentType)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("Content-Type = %q, attendu multipart/form-data", r.ContentType)
	}
	values = map[string]string{}
	mr := multipart.NewReader(bytes.NewReader(r.Body), params["boundary"])
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return names, values
		}
		if err != nil {
			t.Fatalf("multipart illisible : %v", err)
		}
		data, _ := io.ReadAll(part)
		names = append(names, part.FormName())
		values[part.FormName()] = string(data)
	}
}

func TestCloudflareDeployCreatesProject(t *testing.T) {
	files := siteFiles()
	hIndex, hCSS, hPhoto := assetHash(files[0]), assetHash(files[1]), assetHash(files[2])

	f, c := newFakeCloudflare(t, &fakeCloudflare{
		// Sous-domaine différent du nom : c'est lui, et non le nom, qui fait l'URL.
		project: `{"name":"nebula-cafe","subdomain":"nebula-cafe-7xk.pages.dev","production_branch":"main"}`,
		// index.html est déjà connu de Cloudflare ; réponse volontairement hors ordre.
		missing: func([]string) []string { return []string{hCSS, hPhoto} },
	})

	res, err := c.Deploy(context.Background(), cfProject, files)
	if err != nil {
		t.Fatalf("Deploy : %v", err)
	}
	want := Result{
		Provider: "cloudflare",
		URL:      "https://nebula-cafe-7xk.pages.dev",
		DeployID: "dep-cf-1",
		Site:     "nebula-cafe",
		AdminURL: "https://dash.cloudflare.com/acc123/pages/view/nebula-cafe",
	}
	if res != want {
		t.Errorf("Result = %+v\nattendu  %+v", res, want)
	}

	// Séquence exacte, et quel jeton sur quelle route.
	wantCalls := []struct{ key, auth string }{
		{"GET " + cfProjectPath, "Bearer " + cfToken},
		{"POST /accounts/acc123/pages/projects", "Bearer " + cfToken},
		{"GET " + cfProjectPath + "/upload-token", "Bearer " + cfToken},
		{"POST /pages/assets/check-missing", "Bearer " + cfJWT},
		{"POST /pages/assets/upload", "Bearer " + cfJWT},
		{"POST /pages/assets/upsert-hashes", "Bearer " + cfJWT},
		{"POST " + cfProjectPath + "/deployments", "Bearer " + cfToken},
	}
	reqs := f.rec.all()
	if len(reqs) != len(wantCalls) {
		t.Fatalf("appels = %v", f.rec.keys())
	}
	for i, w := range wantCalls {
		if reqs[i].key() != w.key || reqs[i].Auth != w.auth {
			t.Errorf("appel %d = %s [%s]\nattendu   %s [%s]", i+1, reqs[i].key(), reqs[i].Auth, w.key, w.auth)
		}
	}

	// Création du projet.
	var created map[string]any
	if err := json.Unmarshal(reqs[1].Body, &created); err != nil {
		t.Fatalf("corps de création illisible : %v", err)
	}
	if !reflect.DeepEqual(created, map[string]any{"name": "nebula-cafe", "production_branch": "main"}) {
		t.Errorf("création de projet = %v", created)
	}

	// check-missing et upsert-hashes : toutes les empreintes, triées par chemin.
	allHashes := map[string]any{"hashes": []any{hPhoto, hIndex, hCSS}}
	for _, i := range []int{3, 5} {
		var body map[string]any
		if err := json.Unmarshal(reqs[i].Body, &body); err != nil {
			t.Fatalf("%s : corps illisible : %v", reqs[i].key(), err)
		}
		if !reflect.DeepEqual(body, allHashes) {
			t.Errorf("%s = %v\nattendu %v", reqs[i].key(), body, allHashes)
		}
	}

	// upload : uniquement les empreintes manquantes, au format attendu.
	var uploaded []map[string]any
	if err := json.Unmarshal(reqs[4].Body, &uploaded); err != nil {
		t.Fatalf("corps d'upload illisible : %v", err)
	}
	wantUpload := []map[string]any{
		{"key": hPhoto, "value": base64.StdEncoding.EncodeToString(files[2].Data), "metadata": map[string]any{"contentType": "image/jpeg"}, "base64": true},
		{"key": hCSS, "value": base64.StdEncoding.EncodeToString(files[1].Data), "metadata": map[string]any{"contentType": "text/css; charset=utf-8"}, "base64": true},
	}
	if !reflect.DeepEqual(uploaded, wantUpload) {
		t.Errorf("upload = %v\nattendu  %v", uploaded, wantUpload)
	}

	// deployments : multipart, champs manifest et branch, rien d'autre.
	names, values := formFields(t, reqs[6])
	if !reflect.DeepEqual(names, []string{"manifest", "branch"}) {
		t.Errorf("champs multipart = %v, attendu [manifest branch]", names)
	}
	if values["branch"] != "main" {
		t.Errorf("branch = %q, attendu main", values["branch"])
	}
	var manifest map[string]string
	if err := json.Unmarshal([]byte(values["manifest"]), &manifest); err != nil {
		t.Fatalf("manifest illisible : %v", err)
	}
	wantManifest := map[string]string{"/index.html": hIndex, "/styles.css": hCSS, "/assets/photo-1.jpg": hPhoto}
	if !reflect.DeepEqual(manifest, wantManifest) {
		t.Errorf("manifest = %v\nattendu   %v", manifest, wantManifest)
	}
}

func TestCloudflareDeployExistingProject(t *testing.T) {
	// Même contenu, même extension ⇒ une seule empreinte pour deux chemins.
	files := []File{
		{Path: "index.html", Data: []byte("<h1>Bonjour</h1>")},
		{Path: "copie/index.html", Data: []byte("<h1>Bonjour</h1>")},
	}
	h := assetHash(files[0])

	f, c := newFakeCloudflare(t, &fakeCloudflare{
		exists: true,
		// Pas de sous-domaine, et une branche de production qui n'est pas "main".
		project: `{"name":"nebula-cafe","production_branch":"production"}`,
	})
	res, err := c.Deploy(context.Background(), cfProject, files)
	if err != nil {
		t.Fatalf("Deploy : %v", err)
	}
	// Ni création de projet, ni upload (rien ne manque).
	wantCalls := []string{
		"GET " + cfProjectPath,
		"GET " + cfProjectPath + "/upload-token",
		"POST /pages/assets/check-missing",
		"POST /pages/assets/upsert-hashes",
		"POST " + cfProjectPath + "/deployments",
	}
	if got := f.rec.keys(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("appels = %v\nattendu  %v", got, wantCalls)
	}
	if res.URL != "https://a1b2c3d4.nebula-cafe.pages.dev" {
		t.Errorf("URL = %q, attendu le repli sur l'URL du déploiement", res.URL)
	}

	reqs := f.rec.all()
	var checked map[string][]string
	json.Unmarshal(reqs[2].Body, &checked)
	if !reflect.DeepEqual(checked["hashes"], []string{h}) {
		t.Errorf("check-missing = %v, attendu l'empreinte unique %s", checked["hashes"], h)
	}
	_, values := formFields(t, reqs[4])
	if values["branch"] != "production" {
		t.Errorf("branch = %q, attendu la branche de production du projet", values["branch"])
	}
	var manifest map[string]string
	json.Unmarshal([]byte(values["manifest"]), &manifest)
	if want := map[string]string{"/index.html": h, "/copie/index.html": h}; !reflect.DeepEqual(manifest, want) {
		t.Errorf("manifest = %v, attendu %v", manifest, want)
	}
}

func TestCloudflareUploadBatches(t *testing.T) {
	files := []File{{Path: "index.html", Data: []byte("<h1>accueil</h1>")}}
	for i := 1; i < 120; i++ {
		files = append(files, File{Path: fmt.Sprintf("pages/%03d.html", i), Data: []byte(fmt.Sprintf("<h1>page %d</h1>", i))})
	}
	f, c := newFakeCloudflare(t, &fakeCloudflare{
		exists:  true,
		project: `{"name":"nebula-cafe","subdomain":"nebula-cafe.pages.dev","production_branch":"main"}`,
		missing: func(hashes []string) []string { return hashes },
	})
	if _, err := c.Deploy(context.Background(), cfProject, files); err != nil {
		t.Fatalf("Deploy : %v", err)
	}
	var sizes []int
	keys := map[string]bool{}
	for _, r := range f.rec.all() {
		if r.key() != "POST /pages/assets/upload" {
			continue
		}
		var items []struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(r.Body, &items); err != nil {
			t.Fatalf("lot illisible : %v", err)
		}
		sizes = append(sizes, len(items))
		for _, it := range items {
			keys[it.Key] = true
		}
	}
	if !reflect.DeepEqual(sizes, []int{50, 50, 20}) {
		t.Errorf("tailles des lots = %v, attendu [50 50 20]", sizes)
	}
	if len(keys) != 120 {
		t.Errorf("%d empreintes distinctes envoyées, attendu 120", len(keys))
	}
}

func TestSplitBatchesByBytes(t *testing.T) {
	asset := func(n int) cloudflareAsset { return cloudflareAsset{file: File{Data: make([]byte, n)}} }
	// 30 octets ⇒ 40 de base64.
	cases := []struct {
		name     string
		sizes    []int
		maxFiles int
		maxBytes int
		want     []int
	}{
		{"rien", nil, 50, 100, nil},
		{"plafond en octets", []int{30, 30, 30}, 50, 90, []int{2, 1}},
		{"plafond en fichiers", []int{3, 3, 3, 3, 3}, 2, 1000, []int{2, 2, 1}},
		{"fichier plus gros que le lot", []int{30, 300, 30}, 50, 90, []int{1, 1, 1}},
	}
	for _, c := range cases {
		var assets []cloudflareAsset
		for _, n := range c.sizes {
			assets = append(assets, asset(n))
		}
		var got []int
		for _, b := range splitBatches(assets, c.maxFiles, c.maxBytes) {
			got = append(got, len(b))
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s : lots = %v, attendu %v", c.name, got, c.want)
		}
	}
}

func TestAssetHashKnownVectors(t *testing.T) {
	// Ancre indépendante de la bibliothèque : contenu vide et pas d'extension
	// ⇒ on hache la chaîne vide, dont le condensé BLAKE3 de référence
	// (vecteurs officiels) commence par ces 32 caractères.
	if got := assetHash(File{Path: "LICENSE"}); got != "af1349b9f5f9a1a6a0404dea36dcc949" {
		t.Errorf("empreinte du vide = %s, attendu le vecteur officiel BLAKE3", got)
	}

	// Vecteur figé. Le base64 est un littéral écrit à la main : le test ne
	// repose pas sur la manière dont assetHash encode ou concatène.
	f := File{Path: "pages/index.html", Data: []byte("<h1>Bonjour</h1>")}
	const b64 = "PGgxPkJvbmpvdXI8L2gxPg=="
	sum := blake3.Sum256([]byte(b64 + "html"))
	if want := hex.EncodeToString(sum[:])[:32]; assetHash(f) != want {
		t.Errorf("assetHash = %s, attendu blake3(base64 + ext) = %s", assetHash(f), want)
	}
	const frozen = "5087ead67c1bd9490335fa2441f1c7f0"
	if got := assetHash(f); got != frozen {
		t.Errorf("assetHash = %s, attendu la valeur figée %s : l'algorithme a changé", got, frozen)
	}

	// Les variantes plausibles d'une régression doivent toutes différer.
	variants := map[string]string{
		"ordre inversé (ext + base64)": "html" + b64,
		"extension avec le point":      b64 + ".html",
		"sans extension":               b64,
		"contenu brut au lieu du b64":  "<h1>Bonjour</h1>html",
	}
	for name, input := range variants {
		s := blake3.Sum256([]byte(input))
		if hex.EncodeToString(s[:])[:32] == frozen {
			t.Errorf("la variante %q produit la même empreinte", name)
		}
	}
	if len(frozen) != 32 {
		t.Errorf("empreinte de %d caractères, attendu 32", len(frozen))
	}
}

func TestAssetExt(t *testing.T) {
	cases := map[string]string{
		"index.html":           "html",
		"assets/photo-1.jpg":   "jpg",
		"assets/PHOTO.JPG":     "JPG", // wrangler ne change pas la casse
		"archive.tar.gz":       "gz",
		"LICENSE":              "",
		"dossier.v2/README":    "",
		"fin.":                 "",
		".htaccess":            "", // sémantique Node : fichier caché, pas une extension
		".well-known/x.json":   "json",
		"assets/.cache.backup": "backup",
	}
	for p, want := range cases {
		if got := assetExt(p); got != want {
			t.Errorf("assetExt(%q) = %q, attendu %q", p, got, want)
		}
	}
}

func TestContentTypeFor(t *testing.T) {
	cases := map[string]string{
		"index.html":           "text/html; charset=utf-8",
		"styles.css":           "text/css; charset=utf-8",
		"app.js":               "text/javascript; charset=utf-8",
		"assets/photo-1.jpg":   "image/jpeg",
		"assets/LOGO.PNG":      "image/png",
		"assets/icon.svg":      "image/svg+xml",
		"fonts/inter.woff2":    "font/woff2",
		"manifest.webmanifest": "application/manifest+json",
		"LICENSE":              "application/octet-stream",
		"data.zzzunknown":      "application/octet-stream",
	}
	for p, want := range cases {
		if got := contentTypeFor(p); got != want {
			t.Errorf("contentTypeFor(%q) = %q, attendu %q", p, got, want)
		}
	}
}

func TestCloudflareEnvelopeErrors(t *testing.T) {
	existing := `{"name":"nebula-cafe","subdomain":"nebula-cafe.pages.dev","production_branch":"main"}`
	cases := []struct {
		name       string
		exists     bool
		route      string
		respond    func(w http.ResponseWriter)
		wantStatus int
		wantCode   string
		wantMsg    string
		lastCall   string // plus aucun appel après la route en échec
	}{
		{
			name: "jeton refusé sur upload-token", exists: true,
			route:      "GET " + cfProjectPath + "/upload-token",
			respond:    func(w http.ResponseWriter) { cfFail(w, 403, 10000, "Authentication error") },
			wantStatus: 403, wantCode: CodeAuth, wantMsg: "Authentication error",
		},
		{
			name: "quota sur la lecture du projet", exists: true,
			route: "GET " + cfProjectPath,
			respond: func(w http.ResponseWriter) {
				cfFail(w, 429, 971, "Please wait and consider throttling your request speed")
			},
			wantStatus: 429, wantCode: CodeRateLimited, wantMsg: "throttling",
		},
		{
			name:  "création refusée : premier message seulement",
			route: "POST /accounts/acc123/pages/projects",
			respond: func(w http.ResponseWriter) {
				w.WriteHeader(400)
				io.WriteString(w, `{"success":false,"errors":[{"code":8000000,"message":"nom déjà pris"},{"code":1,"message":"second message"}],"result":null}`)
			},
			wantStatus: 400, wantCode: CodeInvalid, wantMsg: "nom déjà pris (code 8000000)",
		},
		{
			name: "success:false sous statut 200", exists: true,
			route: "POST /pages/assets/check-missing",
			respond: func(w http.ResponseWriter) {
				io.WriteString(w, `{"success":false,"errors":[{"code":8000013,"message":"jwt expiré"}],"result":null}`)
			},
			wantStatus: 200, wantCode: CodeUpstream, wantMsg: "jwt expiré",
		},
		{
			name: "500 HTML sur l'upload", exists: true,
			route: "POST /pages/assets/upload",
			respond: func(w http.ResponseWriter) {
				w.WriteHeader(500)
				io.WriteString(w, "<html>oops</html>")
			},
			wantStatus: 500, wantCode: CodeUpstream, wantMsg: "Internal Server Error",
		},
		{
			name: "corps illisible sous statut 200", exists: true,
			route:      "POST " + cfProjectPath + "/deployments",
			respond:    func(w http.ResponseWriter) { io.WriteString(w, "pas du json") },
			wantStatus: 200, wantCode: CodeUpstream, wantMsg: "illisible",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newFakeCloudflare(t, &fakeCloudflare{
				exists:   tc.exists,
				project:  existing,
				missing:  func(h []string) []string { return h },
				override: map[string]func(http.ResponseWriter){tc.route: tc.respond},
			})
			res, err := c.Deploy(context.Background(), cfProject, siteFiles())
			var pe *Error
			if !errors.As(err, &pe) {
				t.Fatalf("erreur = %v, attendu *Error", err)
			}
			if pe.Provider != "cloudflare" || pe.Status != tc.wantStatus || pe.Code != tc.wantCode {
				t.Errorf("Error = %+v, attendu statut %d code %q", pe, tc.wantStatus, tc.wantCode)
			}
			if !strings.Contains(pe.Message, tc.wantMsg) || strings.Contains(pe.Message, "second message") {
				t.Errorf("Message = %q, attendu un fragment %q", pe.Message, tc.wantMsg)
			}
			if strings.Contains(err.Error(), cfToken) || strings.Contains(err.Error(), cfJWT) {
				t.Errorf("un jeton apparaît dans l'erreur : %q", err)
			}
			if res != (Result{}) {
				t.Errorf("Result = %+v, attendu vide", res)
			}
			keys := f.rec.keys()
			if last := keys[len(keys)-1]; last != tc.route {
				t.Errorf("dernier appel = %s : le flux a continué après l'échec de %s", last, tc.route)
			}
		})
	}
}

func TestCloudflareContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f, c := newFakeCloudflare(t, &fakeCloudflare{
		exists:  true,
		project: `{"name":"nebula-cafe","subdomain":"nebula-cafe.pages.dev","production_branch":"main"}`,
		override: map[string]func(http.ResponseWriter){
			"GET " + cfProjectPath + "/upload-token": func(w http.ResponseWriter) {
				cancel()
				cfOK(w, `{"jwt":"`+cfJWT+`"}`)
			},
		},
	})
	_, err := c.Deploy(ctx, cfProject, siteFiles())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("erreur = %v, attendu context.Canceled", err)
	}
	if n := f.rec.count("POST " + cfProjectPath + "/deployments"); n != 0 {
		t.Errorf("déploiement créé malgré l'annulation")
	}
}

func TestCloudflareRejectsBadInputBeforeAnyCall(t *testing.T) {
	cases := []struct {
		name    string
		token   string
		account string
		project string
		files   []File
	}{
		{"fichiers invalides", cfToken, cfAccount, cfProject, []File{{Path: "../index.html"}}},
		{"jeton vide", "", cfAccount, cfProject, siteFiles()},
		{"compte vide", cfToken, "", cfProject, siteFiles()},
		{"compte avec slash", cfToken, "acc/../x", cfProject, siteFiles()},
		{"projet vide", cfToken, cfAccount, "", siteFiles()},
		{"projet en majuscules", cfToken, cfAccount, "Nebula", siteFiles()},
		{"projet accentué", cfToken, cfAccount, "nébula", siteFiles()},
		{"tiret initial", cfToken, cfAccount, "-nebula", siteFiles()},
		{"tiret final", cfToken, cfAccount, "nebula-", siteFiles()},
		{"59 caractères", cfToken, cfAccount, strings.Repeat("a", 59), siteFiles()},
		{"slash", cfToken, cfAccount, "a/b", siteFiles()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newFakeCloudflare(t, &fakeCloudflare{exists: true, project: `{}`})
			c.Token, c.AccountID = tc.token, tc.account
			_, err := c.Deploy(context.Background(), tc.project, tc.files)
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("erreur = %v, attendu ErrInvalidInput", err)
			}
			if got := f.rec.keys(); len(got) != 0 {
				t.Errorf("appels réseau malgré une entrée invalide : %v", got)
			}
		})
	}
}

func TestSanitizeProjectName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Nébula Café !", "nebula-cafe"},
		{"  L'Atelier d'Élise  ", "l-atelier-d-elise"},
		{"ÉCOLE Œuvre & Cœur", "ecole-oeuvre-coeur"},
		{"Café Noël", "cafe-noel"}, // accents combinants (forme décomposée)
		{"Garçon — Über Straße", "garcon-uber-strasse"},
		{"my_site.v2", "my-site-v2"},
		{"nebula-cafe-2", "nebula-cafe-2"},
		{"58 max " + strings.Repeat("a", 80), "58-max-" + strings.Repeat("a", 51)},
		{strings.Repeat("a", 57) + "-bbbb", strings.Repeat("a", 57)}, // la coupe ne laisse pas de tiret final
		{"", "nadaprod-site"},
		{"---", "nadaprod-site"},
		{"!!! ???", "nadaprod-site"},
		{"日本語", "nadaprod-site"},
	}
	for _, c := range cases {
		got := SanitizeProjectName(c.in)
		if got != c.want {
			t.Errorf("SanitizeProjectName(%q) = %q, attendu %q", c.in, got, c.want)
		}
		if !projectNameRe.MatchString(got) {
			t.Errorf("SanitizeProjectName(%q) = %q : nom de projet invalide", c.in, got)
		}
	}
}
