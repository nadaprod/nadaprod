package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"nadaprod/pkg/prompts"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCleanHTML(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"nu", "<section>ok</section>", "<section>ok</section>"},
		{"clôture html", "```html\n<section>ok</section>\n```", "<section>ok</section>"},
		{"clôture sans langage", "```\n<div>x</div>\n```", "<div>x</div>"},
		{"bavardage autour", "Voici le bloc :\n<header>h</header>\nJ'espère que ça convient.", "<header>h</header>"},
		{"clôture + bavardage", "Bien sûr !\n```html\n<footer>f</footer>\n```\nBonne journée", "<footer>f</footer>"},
		{"clôture jamais refermée (troncature)", "```html\n<section><p>coupé</p>", "<section><p>coupé</p>"},
		{"espaces", "  \n<nav>n</nav>\n  ", "<nav>n</nav>"},
		{"vide", "   ", ""},
		{"aucune balise", "désolé, je ne peux pas", "désolé, je ne peux pas"},
	}
	for _, c := range cases {
		if got := cleanHTML(c.in); got != c.want {
			t.Errorf("%s : %q, attendu %q", c.name, got, c.want)
		}
	}
}

func TestSanitize(t *testing.T) {
	in := `<section><p>a</p><script>alert(1)</script><SCRIPT src="x.js"></SCRIPT></section>`
	if got := Sanitize(in, false); strings.Contains(strings.ToLower(got), "script") {
		t.Errorf("scripts non retirés : %s", got)
	}
	if got := Sanitize(in, true); got != in {
		t.Errorf("un bloc autorisé à embarquer du JS doit rester intact : %s", got)
	}
}

func TestBuildUserMessage(t *testing.T) {
	spec, _ := prompts.GetBlockSpec("hero")

	msg := buildUserMessage(spec, GenerateRequest{Block: "hero"})
	if !strings.Contains(msg, "Aucune consigne particulière") || strings.Contains(msg, "Contexte de marque") ||
		strings.Contains(msg, "Design system") || !strings.HasSuffix(msg, "fragment HTML final.") {
		t.Errorf("message minimal inattendu :\n%s", msg)
	}

	req := GenerateRequest{
		Block: "hero", Prompt: "Hero sombre", CurrentHTML: "<section>old</section>",
		Brand:    Brand{Name: "Nébula", Colors: "violet", Tone: "SaaS"},
		Siblings: []Sibling{{Type: "header", HTML: `<header><a class="bg-violet-600 px-4">Commencer</a></header>`}},
	}
	msg = buildUserMessage(spec, req)
	for _, want := range []string{"Hero sombre", "- Nom : Nébula", "- Palette : violet", "- Ton : SaaS",
		"Cohérence avec le reste de la page", "« Commencer »", "```html\n<section>old</section>\n```", "ne repars pas de zéro"} {
		if !strings.Contains(msg, want) {
			t.Errorf("il manque %q dans :\n%s", want, msg)
		}
	}

	// Design system actif : jetons imposés, palette libre écartée.
	req.Theme = &Theme{Enabled: true, Mode: "dark", Primary: "#7C3AED", FontDisplay: "Sora", Radius: "lg", Style: "éditorial"}
	msg = buildUserMessage(spec, req)
	for _, want := range []string{"primary-50 à primary-950", "font-display", "rounded-brand", "arrondi généreux", "page SOMBRE", "éditorial", "display = Sora"} {
		if !strings.Contains(msg, want) {
			t.Errorf("thème : il manque %q", want)
		}
	}
	if strings.Contains(msg, "- Palette : violet") {
		t.Error("la palette libre ne doit pas contredire le design system")
	}

	// Variante : direction imposée + contenu conservé.
	req.Variant = 2
	msg = buildUserMessage(spec, req)
	if !strings.Contains(msg, "Direction B") || !strings.Contains(msg, "mise en forme franchement différente") {
		t.Errorf("variante :\n%s", msg)
	}

	// Re-thème : pas de brief, pas de variante, pas de résumé des voisins.
	req.Mode = ModeRetheme
	msg = buildUserMessage(spec, req)
	if !strings.Contains(msg, "RE-THÈME") || strings.Contains(msg, "Hero sombre") ||
		strings.Contains(msg, "Direction B") || strings.Contains(msg, "Cohérence avec") {
		t.Errorf("re-thème :\n%s", msg)
	}
	// Re-thème sans HTML courant = génération normale.
	req.CurrentHTML = ""
	if msg = buildUserMessage(spec, req); strings.Contains(msg, "RE-THÈME") {
		t.Error("re-thème sans version actuelle : doit retomber sur le brief")
	}
}

func TestSummarizeSiblings(t *testing.T) {
	if s := SummarizeSiblings(nil); s != "" {
		t.Errorf("aucun voisin : %q", s)
	}
	if s := SummarizeSiblings([]Sibling{{Type: "hero", HTML: "  "}}); s != "" {
		t.Errorf("voisins vides : %q", s)
	}
	sum := SummarizeSiblings([]Sibling{
		{Type: "header", HTML: `<header class="bg-slate-950"><a href="#" class="rounded bg-violet-600 text-white">Essai <b>gratuit</b></a></header>`},
		{Type: "hero", HTML: `<section><h1 class="text-violet-600">Le <span>CRM</span> simple</h1><button class="bg-violet-600">Essai gratuit</button><a class="text-sky-500">Doc</a></section>`},
	})
	for _, want := range []string{"- header (fond sombre)", "CTA : « Essai gratuit »", "titres : « Le CRM simple »", "Teintes dominantes déjà utilisées : violet-600, sky-500"} {
		if !strings.Contains(sum, want) {
			t.Errorf("il manque %q dans :\n%s", want, sum)
		}
	}
	if strings.Contains(sum, "slate-950") {
		t.Error("les neutres ne sont pas des teintes de marque")
	}
	big := make([]Sibling, 40)
	for i := range big {
		big[i] = Sibling{Type: "features", HTML: "<section><h2>" + strings.Repeat("Titre très long ", 20) + "</h2></section>"}
	}
	if n := len([]rune(SummarizeSiblings(big))); n > 1501 {
		t.Errorf("résumé non borné : %d caractères", n)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   string
		retry  int
	}{
		{429, `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"23s"}]}}`, "quota", 23},
		{400, `{"error":{"code":400,"message":"API key not valid","status":"INVALID_ARGUMENT","details":[{"reason":"API_KEY_INVALID"}]}}`, "auth", 0},
		{403, `{"error":{"code":403,"message":"denied","status":"PERMISSION_DENIED"}}`, "auth", 0},
		{404, `{"error":{"code":404,"message":"model not found","status":"NOT_FOUND"}}`, "invalid", 0},
		{503, `<html>bad gateway</html>`, "upstream", 0},
	}
	for _, c := range cases {
		e := classify(c.status, []byte(c.body))
		if e.Code != c.code || e.RetryAfter != c.retry || e.Status != c.status {
			t.Errorf("%d : %+v, attendu code=%s retry=%d", c.status, e, c.code, c.retry)
		}
	}
}

// fakeGemini : serveur qui répond selon une liste de scénarios successifs.
func fakeGemini(t *testing.T, handlers ...http.HandlerFunc) (*Client, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(atomic.AddInt32(&calls, 1)) - 1
		if r.Header.Get("x-goog-api-key") != "k" {
			t.Errorf("clé API absente de l'en-tête")
		}
		if i >= len(handlers) {
			i = len(handlers) - 1
		}
		handlers[i](w, r)
	}))
	t.Cleanup(srv.Close)
	return NewClient("k", "flash-test", Options{Endpoint: srv.URL}), &calls
}

func jsonCandidate(text string) string {
	return fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"text":%q}]},"finishReason":"STOP"}]}`, text)
}

func TestGenerateBlock(t *testing.T) {
	spec, _ := prompts.GetBlockSpec("features") // JS interdit
	ok := func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/flash-test:generateContent") {
			t.Errorf("chemin : %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "SPÉCIALISATION — FONCTIONNALITÉS") || !strings.Contains(string(body), `"temperature":0.7`) {
			t.Errorf("corps de requête : %s", body)
		}
		io.WriteString(w, jsonCandidate("```html\n<section>ok<script>x()</script></section>\n```"))
	}

	c, calls := fakeGemini(t, ok)
	html, err := c.GenerateBlock(context.Background(), spec, GenerateRequest{Block: "features"})
	if err != nil || html != "<section>ok</section>" || *calls != 1 {
		t.Fatalf("html=%q err=%v calls=%d", html, err, *calls)
	}

	// Erreur définitive : pas de retry.
	c, calls = fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"code":400,"message":"API key not valid","details":[{"reason":"API_KEY_INVALID"}]}}`)
	})
	_, err = c.GenerateBlock(context.Background(), spec, GenerateRequest{Block: "features"})
	var ge *Error
	if !errors.As(err, &ge) || ge.Code != "auth" || *calls != 1 {
		t.Fatalf("err=%v calls=%d", err, *calls)
	}

	// Refus du modèle.
	c, _ = fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"promptFeedback":{"blockReason":"SAFETY"}}`)
	})
	_, err = c.GenerateBlock(context.Background(), spec, GenerateRequest{Block: "features"})
	if !errors.As(err, &ge) || ge.Code != "blocked" || ge.HTTPStatus() != 422 {
		t.Fatalf("blocage : %v", err)
	}

	// Réponse vide.
	c, _ = fakeGemini(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"candidates":[]}`) })
	_, err = c.GenerateBlock(context.Background(), spec, GenerateRequest{Block: "features"})
	if !errors.As(err, &ge) || ge.Code != "empty" {
		t.Fatalf("vide : %v", err)
	}

	// Annulation par le client pendant l'attente du retry.
	c, _ = fakeGemini(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.GenerateBlock(ctx, spec, GenerateRequest{Block: "features"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("annulation : %v", err)
	}
}

func TestStreamBlock(t *testing.T) {
	spec, _ := prompts.GetBlockSpec("header") // JS autorisé
	sse := func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, ":streamGenerateContent") || r.URL.Query().Get("alt") != "sse" {
			t.Errorf("URL de flux : %s", r.URL)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, part := range []string{"```html\n<header>", "<nav>n</nav>", "<script>m()</script></header>\n```"} {
			fmt.Fprintf(w, "data: %s\n\n", jsonCandidate(part))
			w.(http.Flusher).Flush()
		}
	}
	c, _ := fakeGemini(t, sse)
	var deltas []string
	html, err := c.StreamBlock(context.Background(), spec, GenerateRequest{Block: "header", Variant: 1}, func(s string) { deltas = append(deltas, s) })
	if err != nil || html != "<header><nav>n</nav><script>m()</script></header>" {
		t.Fatalf("html=%q err=%v", html, err)
	}
	if len(deltas) != 3 || deltas[1] != "<nav>n</nav>" {
		t.Fatalf("deltas : %q", deltas)
	}

	// 429 avant tout envoi : un retry (≈ 1 s) puis le flux aboutit.
	c, calls := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED"}}`)
	}, sse)
	html, err = c.StreamBlock(context.Background(), spec, GenerateRequest{Block: "header"}, nil)
	if err != nil || !strings.HasPrefix(html, "<header>") || *calls != 2 {
		t.Fatalf("retry du flux : html=%q err=%v calls=%d", html, err, *calls)
	}

	// 429 persistant : l'erreur finale est typée et porte le délai conseillé.
	c, _ = fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED","details":[{"retryDelay":"7s"}]}}`)
	})
	_, err = c.StreamBlock(context.Background(), spec, GenerateRequest{Block: "header"}, nil)
	var ge *Error
	if !errors.As(err, &ge) || ge.Code != "quota" || ge.RetryAfter != 7 || ge.HTTPStatus() != 429 {
		t.Fatalf("quota : %v", err)
	}
}

func TestBuildUserMessageLang(t *testing.T) {
	spec, _ := prompts.GetBlockSpec("hero")
	base := GenerateRequest{Block: "hero", Prompt: "Hero", Brand: Brand{Name: "N"}, CurrentHTML: "<section>x</section>"}
	ref := buildUserMessage(spec, base)
	for _, lang := range []string{"", "fr", " FR "} {
		r := base
		r.Lang = lang
		if got := buildUserMessage(spec, r); got != ref {
			t.Errorf("lang %q : le message doit rester identique", lang)
		}
	}
	r := base
	r.Lang = "en"
	msg := buildUserMessage(spec, r)
	for _, want := range []string{"Langue de la page : anglais (en)", "remplace celle du « copywriting français »", "un seul élément"} {
		if !strings.Contains(msg, want) {
			t.Errorf("section de langue sans %q :\n%s", want, msg)
		}
	}
	r.Mode, r.Lang = ModeRetheme, "es"
	if strings.Contains(buildUserMessage(spec, r), "Langue de la page") {
		t.Error("un re-thème conserve les textes : pas de consigne de langue")
	}
	if langName("pt-br") != "portugais (pt-br)" || langName("xx") != "xx" {
		t.Errorf("langName : %q %q", langName("pt-br"), langName("xx"))
	}
}

func TestTranslate(t *testing.T) {
	items := map[string]string{}
	for i := range 85 {
		items[fmt.Sprintf("s%03d", i)] = fmt.Sprintf("Bonjour <1>n°%d</1>", i)
	}
	var sizes []int
	echo := func(w http.ResponseWriter, r *http.Request) {
		var req gRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.GenerationConfig.ResponseMimeType != "application/json" || req.GenerationConfig.Temperature > 0.3 {
			t.Errorf("config : %+v", req.GenerationConfig)
		}
		if req.SystemInstruction.Parts[0].Text != prompts.TranslatorPrompt {
			t.Error("prompt système de traduction absent")
		}
		msg := req.Contents[0].Parts[0].Text
		if !strings.Contains(msg, "Langue cible : anglais (en)") || !strings.Contains(msg, "Marque (ne pas traduire) : Nébula") {
			t.Errorf("message : %s", msg)
		}
		var in map[string]string
		json.Unmarshal([]byte(msg[strings.Index(msg, "{"):]), &in)
		sizes = append(sizes, len(in))
		out := map[string]string{"inconnu": "x"}
		for id, s := range in {
			out[id] = strings.Replace(s, "Bonjour", "Hello", 1)
		}
		data, _ := json.Marshal(out)
		io.WriteString(w, jsonCandidate("```json\n"+string(data)+"\n```"))
	}
	c, calls := fakeGemini(t, echo)
	got, err := c.Translate(context.Background(), TranslateRequest{From: "fr", To: "en", Brand: Brand{Name: "Nébula"}, Items: items})
	if err != nil || *calls != 3 || fmt.Sprint(sizes) != "[40 40 5]" {
		t.Fatalf("err=%v calls=%d lots=%v", err, *calls, sizes)
	}
	if len(got) != 85 || got["s007"] != "Hello <1>n°7</1>" || got["inconnu"] != "" {
		t.Fatalf("traductions : %d, s007=%q", len(got), got["s007"])
	}

	// Ids manquants et chaînes vides : absents du résultat ; JSON illisible : erreur typée.
	c, _ = fakeGemini(t,
		func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, jsonCandidate(`{"a":"A","b":"  "}`)) },
		func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, jsonCandidate(`pas du json`)) })
	got, err = c.Translate(context.Background(), TranslateRequest{From: "fr", To: "en", Items: map[string]string{"a": "a", "b": "b", "c": "c"}})
	if err != nil || len(got) != 1 || got["a"] != "A" {
		t.Fatalf("partiel : %v %v", got, err)
	}
	_, err = c.Translate(context.Background(), TranslateRequest{From: "fr", To: "en", Items: map[string]string{"a": "a"}})
	var ge *Error
	if !errors.As(err, &ge) || ge.Code != "upstream" {
		t.Fatalf("illisible : %v", err)
	}

	// Contexte annulé : aucun appel.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, calls = fakeGemini(t, echo)
	if _, err := c.Translate(ctx, TranslateRequest{Items: items}); !errors.Is(err, context.Canceled) || *calls != 0 {
		t.Fatalf("annulation : %v calls=%d", err, *calls)
	}
}

func TestOnUsage(t *testing.T) {
	var events []UsageEvent
	record := func(c *Client) *Client { c.opts.OnUsage = func(e UsageEvent) { events = append(events, e) }; return c }
	withUsage := func(text string, prompt, out, thoughts int) string {
		return fmt.Sprintf(`{"candidates":[{"content":{"parts":[{"text":%q}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":%d,"candidatesTokenCount":%d,"thoughtsTokenCount":%d,"totalTokenCount":0}}`, text, prompt, out, thoughts)
	}
	hero, _ := prompts.GetBlockSpec("hero")

	// Appel simple : tokens lus, réflexion comptée en sortie.
	c, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, withUsage("<section>ok</section>", 120, 300, 40))
	})
	if _, err := record(c).GenerateBlock(context.Background(), hero, GenerateRequest{Block: "hero"}); err != nil {
		t.Fatal(err)
	}
	if e := events[0]; e.Kind != "hero" || e.Model != "flash-test" || !e.OK || e.PromptTokens != 120 || e.OutputTokens != 340 || e.Duration <= 0 {
		t.Fatalf("simple : %+v", e)
	}

	// Flux : un seul événement, compteurs du dernier fragment qui les porte.
	c, _ = fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "data: %s\n\n", jsonCandidate("<section>"))
		fmt.Fprintf(w, "data: %s\n\n", withUsage("a</section>", 50, 9, 0))
	})
	if _, err := record(c).StreamBlock(context.Background(), hero, GenerateRequest{Block: "hero"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].PromptTokens != 50 || events[1].OutputTokens != 9 || !events[1].OK {
		t.Fatalf("flux : %+v", events)
	}

	// Échec définitif (après retries) : un événement OK=false, une seule fois.
	c, _ = fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"bad"}}`)
	})
	if _, err := record(c).GenerateBlock(context.Background(), hero, GenerateRequest{Block: "hero"}); err == nil {
		t.Fatal("erreur attendue")
	}
	if len(events) != 3 || events[2].OK {
		t.Fatalf("échec : %+v", events)
	}

	// Traduction : un événement par lot, kind "translate".
	c, _ = fakeGemini(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, withUsage(`{"a":"A"}`, 10, 5, 0)) })
	if _, err := record(c).Translate(context.Background(), TranslateRequest{From: "fr", To: "en", Items: map[string]string{"a": "a"}}); err != nil {
		t.Fatal(err)
	}
	if e := events[3]; len(events) != 4 || e.Kind != "translate" || !e.OK || e.OutputTokens != 5 {
		t.Fatalf("traduction : %+v", events)
	}

	// Sans OnUsage : rien ne casse.
	c, _ = fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, withUsage("<section>ok</section>", 1, 1, 0))
	})
	if _, err := c.GenerateBlock(context.Background(), hero, GenerateRequest{Block: "hero"}); err != nil {
		t.Fatal(err)
	}
}
