package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Publication d'un site importé.
//
// La source éditable vit dans sites/<id>/dev/ ; la sortie publiée dans
// sites/<id>/public/, servie en priorité sur un domaine branché (avec repli
// sur dev/ pour tout ce qui n'est pas transformé — les assets ne sont donc
// jamais dupliqués). Trois transformations, chacune débrayable via site.json :
//
//  1. compile — remplace cdn.tailwindcss.com par un CSS compilé localement
//     (binaire standalone Tailwind v3, TAILWIND_BIN), config inline honorée ;
//  2. pwa     — manifest + service worker + icône générés, enregistrement
//     injecté (sauf si le site fournit déjà les siens) ;
//  3. gdpr    — bandeau « Zéro stress : tout est local » avec lien légal.
//
// En cas d'échec de compilation, la page garde son CDN : dégradation douce.

const buildTimeout = 60 * time.Second

var (
	// <script src="https://cdn.tailwindcss.com"></script> (160/163 occurrences
	// observées sont exactement cette forme ; on tolère querystring et attributs).
	cdnTagRe = regexp.MustCompile(`(?i)<script[^>]*\bsrc\s*=\s*["']https?://cdn\.tailwindcss\.com[^"']*["'][^>]*>\s*</script>`)
	// Tous les <script> inline — on cherche celui qui affecte tailwind.config.
	scriptTagRe = regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>(.*?)</script>`)
	twAssignRe  = regexp.MustCompile(`tailwind\.config\s*=`)
	// Un script « strict » ne contient QUE l'affectation → supprimable tel quel.
	strictCfgRe = regexp.MustCompile(`(?s)^\s*tailwind\.config\s*=\s*\{.*\}\s*;?\s*$`)

	headCloseRe   = regexp.MustCompile(`(?i)</head>`)
	manifestRelRe = regexp.MustCompile(`(?i)rel\s*=\s*["']?manifest`)
)

const (
	pwaRegisterTag = `<script id="__np_pwa">if('serviceWorker' in navigator&&(location.protocol==='https:'||location.hostname==='localhost'))navigator.serviceWorker.register('/sw.js');</script>`
	manifestTag    = `<link id="__np_manifest" rel="manifest" href="/manifest.webmanifest">`

	// gdprTemplate : le %s reçoit le dictionnaire JSON des traductions (TR).
	// Résolution de langue, dans l'ordre : paramètre GET ?lang= (mémorisé),
	// choix mémorisé, langue du navigateur, puis repli sur TR.en.
	gdprTemplate = `<script id="__np_gdpr">(function(){try{if(localStorage.getItem('np-gdpr-ok'))return}catch(e){return}
var TR=%s;
var q=null;try{q=new URLSearchParams(location.search).get('lang')}catch(e){}
var lang=q;
try{if(q)localStorage.setItem('np-lang',q);else lang=localStorage.getItem('np-lang')}catch(e){}
if(!lang)lang=(navigator.languages&&navigator.languages[0])||navigator.language||'en';
lang=String(lang).slice(0,2).toLowerCase();
var m=TR[lang]||TR.en;
var d=document.createElement('div');d.setAttribute('role','dialog');d.setAttribute('aria-label',m.a);
d.style.cssText='position:fixed;left:16px;bottom:16px;z-index:2147483647;max-width:320px;background:#0A0C11;color:#E7EAF1;border:1px solid #2A3245;border-radius:14px;padding:14px 16px;font:13px/1.5 system-ui,sans-serif;box-shadow:0 12px 40px rgba(0,0,0,.45)';
d.innerHTML='<strong style="display:block;margin-bottom:4px">'+m.t+'</strong>'+m.b+' <a href="https://nadaprod.com/legal" target="_blank" rel="noopener" style="color:#A78BFA">'+m.l+'</a><div style="text-align:right;margin-top:10px"><button type="button" style="background:#7C3AED;color:#fff;border:0;border-radius:9px;padding:6px 18px;font:600 13px system-ui,sans-serif;cursor:pointer">'+m.k+'</button></div>';
d.querySelector('button').onclick=function(){try{localStorage.setItem('np-gdpr-ok','1')}catch(e){}d.remove()};
(document.body||document.documentElement).appendChild(d)})();</script>`
)

// ---------- Bandeau RGPD : traductions embarquées ----------
//
// Les sites publiés sont autonomes (ils ne peuvent pas atteindre les JSON de
// l'app) : le dictionnaire est donc embarqué dans le <script> injecté, lu au
// démarrage depuis web/static/i18n/<lang>.json (clés gdpr.*). Ajouter une
// langue là-bas suffit — embarquée au prochain démarrage + rebuild.

// gdprText : les textes d'une langue (clés courtes → JSON embarqué minuscule).
type gdprText struct {
	Aria  string `json:"a"`
	Title string `json:"t"`
	Body  string `json:"b"`
	Legal string `json:"l"`
	OK    string `json:"k"`
}

// Repli si le dossier i18n est absent : le bandeau fonctionne toujours.
var gdprDefaults = map[string]gdprText{
	"fr": {"Confidentialité", "Zéro stress : tout est local.", "Ce site ne dépose aucun cookie de suivi et ne collecte aucune donnée personnelle.", "Mentions légales", "OK"},
	"en": {"Privacy", "Zero stress: everything stays local.", "This site sets no tracking cookies and collects no personal data.", "Legal notice", "OK"},
}

func gdprTranslations(i18nDir string) map[string]gdprText {
	trs := map[string]gdprText{}
	for k, v := range gdprDefaults {
		trs[k] = v
	}
	entries, err := os.ReadDir(i18nDir)
	if err != nil {
		return trs
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(i18nDir, e.Name()))
		if err != nil {
			continue
		}
		var dict map[string]string
		if json.Unmarshal(b, &dict) != nil {
			continue
		}
		tx := gdprText{Aria: dict["gdpr.aria"], Title: dict["gdpr.title"], Body: dict["gdpr.body"], Legal: dict["gdpr.legal"], OK: dict["gdpr.ok"]}
		if tx.Title == "" || tx.Body == "" || tx.Legal == "" {
			continue // langue sans bandeau traduit : ignorée
		}
		if tx.Aria == "" {
			tx.Aria = "Privacy"
		}
		if tx.OK == "" {
			tx.OK = "OK"
		}
		trs[strings.TrimSuffix(e.Name(), ".json")] = tx
	}
	return trs
}

// buildGDPRTag assemble le <script> injecté. json.Marshal échappe < > & :
// le dictionnaire embarqué ne peut pas casser le HTML environnant.
func buildGDPRTag(trs map[string]gdprText) string {
	b, _ := json.Marshal(trs)
	return fmt.Sprintf(gdprTemplate, b)
}

// swTemplate : cache versionné (les anciens caches np-* sont purgés à
// l'activation), network-first pour les navigations (repli hors-ligne sur le
// cache), stale-while-revalidate pour les assets. Même origine uniquement.
const swTemplate = `const CACHE='%s';
self.addEventListener('install',()=>self.skipWaiting());
self.addEventListener('activate',e=>e.waitUntil(caches.keys().then(ks=>Promise.all(ks.filter(k=>k!==CACHE&&k.startsWith('np-')).map(k=>caches.delete(k)))).then(()=>self.clients.claim())));
self.addEventListener('fetch',e=>{
  const req=e.request;
  if(req.method!=='GET'||new URL(req.url).origin!==location.origin)return;
  const put=r=>{if(r&&r.ok){const c=r.clone();caches.open(CACHE).then(x=>x.put(req,c))}return r};
  if(req.mode==='navigate'){
    e.respondWith(fetch(req).then(put).catch(()=>caches.match(req).then(h=>h||caches.match('/'))));
  }else{
    e.respondWith(caches.match(req).then(hit=>{
      const net=fetch(req).then(put).catch(()=>hit);
      return hit||net;
    }));
  }
});
`

// ---------- Réécritures HTML (fonctions pures, testées dans build_test.go) ----------

// insertBefore insère ins avant la première occurrence de re, ou en fin de
// document si le marqueur n'existe pas (même convention que serveEdit).
func insertBefore(raw []byte, re *regexp.Regexp, ins string) []byte {
	if loc := re.FindIndex(raw); loc != nil {
		out := make([]byte, 0, len(raw)+len(ins))
		out = append(out, raw[:loc[0]]...)
		out = append(out, ins...)
		out = append(out, raw[loc[0]:]...)
		return out
	}
	return append(append([]byte{}, raw...), ins...)
}

// extractTailwindConfig repère le premier <script> inline contenant une
// affectation tailwind.config. strict = le script ne contient rien d'autre
// (il peut alors être retiré de la page compilée).
func extractTailwindConfig(raw []byte) (body string, strict bool, found bool) {
	for _, m := range scriptTagRe.FindAllSubmatchIndex(raw, -1) {
		b := raw[m[2]:m[3]]
		if twAssignRe.Match(b) {
			return string(b), strictCfgRe.Match(b), true
		}
	}
	return "", false, false
}

// rewriteTailwindHTML remplace le tag CDN par le CSS compilé inline.
// Le script de config strict est retiré ; un script mixte est conservé mais
// neutralisé (window.tailwind défini pour éviter la ReferenceError).
func rewriteTailwindHTML(raw, css []byte) []byte {
	_, strict, found := extractTailwindConfig(raw)
	repl := "<style data-np-tailwind>" + string(css) + "</style>"
	if found && !strict {
		repl += `<script>window.tailwind=window.tailwind||{config:{}};</script>`
	}
	first := true
	out := cdnTagRe.ReplaceAllFunc(raw, func(_ []byte) []byte {
		if first {
			first = false
			return []byte(repl)
		}
		return nil // tags CDN surnuméraires : supprimés
	})
	if found && strict {
		done := false
		out = scriptTagRe.ReplaceAllFunc(out, func(tag []byte) []byte {
			if !done {
				if m := scriptTagRe.FindSubmatch(tag); m != nil && twAssignRe.Match(m[1]) {
					done = true
					return nil
				}
			}
			return tag
		})
	}
	return out
}

// ---------- Compilation Tailwind (binaire standalone) ----------

// compileTailwind compile le CSS purgé d'une page via le binaire standalone.
// cfgBody est le contenu du <script> de config inline (vide si absent) : il
// est exécuté dans le config JS, tailwind.config y étant un objet neutre.
func compileTailwind(twBin, pagePath, cfgBody string) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "np-tw-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	cfg := "const tailwind = {};\n" + cfgBody + "\n;module.exports = tailwind.config || {};\n"
	if err := os.WriteFile(filepath.Join(tmp, "config.cjs"), []byte(cfg), 0o644); err != nil {
		return nil, err
	}
	input := "@tailwind base;\n@tailwind components;\n@tailwind utilities;\n"
	if err := os.WriteFile(filepath.Join(tmp, "input.css"), []byte(input), 0o644); err != nil {
		return nil, err
	}
	outPath := filepath.Join(tmp, "out.css")

	ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, twBin,
		"-c", filepath.Join(tmp, "config.cjs"),
		"-i", filepath.Join(tmp, "input.css"),
		"--content", pagePath,
		"-o", outPath,
		"--minify")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tailwindcss : %v — %s", err, strings.TrimSpace(stderr.String()))
	}
	return os.ReadFile(outPath)
}

// ---------- Orchestration par site ----------

// buildLock sérialise les builds d'un même site (upload, save, réglages).
func (s *Store) buildLock(id string) *sync.Mutex {
	m, _ := s.buildLocks.LoadOrStore(id, &sync.Mutex{})
	return m.(*sync.Mutex)
}

type buildFlags struct {
	compile     bool // binaire présent ET réglage actif
	fonts       bool // miroir présent ET réglage actif
	gdpr        bool
	genSW       bool // PWA actif et le site n'apporte pas son propre SW racine
	genManifest bool // PWA actif et pas de manifest fourni par le site
}

func (s *Store) flagsFor(id string, meta Meta) buildFlags {
	src := s.srcDir(id)
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(src, name))
		return err == nil
	}
	pwa := meta.PWAOn()
	return buildFlags{
		compile:     meta.CompileOn() && s.twBin != "",
		fonts:       meta.FontsOn() && s.fontsDir != "",
		gdpr:        meta.GDPROn(),
		genSW:       pwa && !has("sw.js") && !has("service-worker.js"),
		genManifest: pwa && !has("manifest.webmanifest") && !has("manifest.json"),
	}
}

// buildPage transforme une page de dev/ vers public/. Retourne true si un
// fichier a été écrit (sinon le repli sur dev/ suffit).
func (s *Store) buildPage(id, rel string, f buildFlags) (bool, error) {
	srcPath, err := safeJoin(s.srcDir(id), rel)
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return false, err
	}
	out := raw
	changed := false

	if f.compile && cdnTagRe.Match(out) {
		cfgBody, _, _ := extractTailwindConfig(out)
		css, err := compileTailwind(s.twBin, srcPath, cfgBody)
		if err != nil {
			// Dégradation douce : la page publiée garde le CDN.
			log.Printf("build %s/%s : compilation Tailwind échouée, CDN conservé : %v", id, rel, err)
		} else {
			out = rewriteTailwindHTML(out, css)
			changed = true
		}
	}
	if f.fonts {
		if rewritten, ok := s.localizeFonts(id, out); ok {
			out = rewritten
			changed = true
		}
	}
	if f.genManifest && !manifestRelRe.Match(out) {
		out = insertBefore(out, headCloseRe, manifestTag)
		changed = true
	}
	if f.genSW && !bytes.Contains(out, []byte("serviceWorker.register")) {
		out = insertBefore(out, bodyCloseRe, pwaRegisterTag)
		changed = true
	}
	if f.gdpr {
		tag := s.gdprTag
		if tag == "" { // store construit à la main (tests) : replis embarqués
			tag = buildGDPRTag(gdprDefaults)
		}
		out = insertBefore(out, bodyCloseRe, tag)
		changed = true
	}
	if !changed {
		return false, nil
	}
	dst, err := safeJoin(s.publicDir(id), rel)
	if err != nil {
		return false, err
	}
	return true, writeFile(dst, bytes.NewReader(out))
}

// writePWAFiles génère sw.js, manifest.webmanifest et l'icône dans public/.
func (s *Store) writePWAFiles(id string, meta Meta, f buildFlags) error {
	pub := s.publicDir(id)
	if f.genSW {
		sw := fmt.Sprintf(swTemplate, fmt.Sprintf("np-%s-%d", id, time.Now().Unix()))
		if err := writeFile(filepath.Join(pub, "sw.js"), strings.NewReader(sw)); err != nil {
			return err
		}
	}
	if f.genManifest {
		name := meta.Domain
		if name == "" {
			name = id
		}
		manifest, _ := json.MarshalIndent(map[string]any{
			"name": name, "short_name": name,
			"start_url": "/", "scope": "/", "display": "standalone",
			"background_color": "#0A0C11", "theme_color": "#0A0C11",
			"icons": []map[string]string{{"src": "/np-icon.svg", "sizes": "any", "type": "image/svg+xml"}},
		}, "", "  ")
		if err := writeFile(filepath.Join(pub, "manifest.webmanifest"), bytes.NewReader(manifest)); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(pub, "np-icon.svg"), strings.NewReader(iconSVG(id))); err != nil {
			return err
		}
	}
	return nil
}

// iconSVG : icône minimaliste générée — première lettre du site.
func iconSVG(id string) string {
	letter := 'N'
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letter = unicode.ToUpper(r)
			break
		}
	}
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><rect width="512" height="512" rx="96" fill="#0A0C11"/><circle cx="256" cy="256" r="180" fill="none" stroke="#7C3AED" stroke-width="24"/><text x="256" y="330" font-family="system-ui,sans-serif" font-size="210" font-weight="700" fill="#E7EAF1" text-anchor="middle">%c</text></svg>`, letter)
}

// BuildSite reconstruit intégralement public/ depuis dev/. public/ ne
// contient que des artefacts régénérables : on repart de zéro à chaque fois
// (pendant la fenêtre de rebuild, le repli sur dev/ sert la version CDN).
func (s *Store) BuildSite(id string) {
	mu := s.buildLock(id)
	mu.Lock()
	defer mu.Unlock()

	src, pub := s.srcDir(id), s.publicDir(id)
	if src == pub {
		return // site non migré (pas de dev/) : ne jamais écraser la source
	}
	meta := s.meta(id)
	f := s.flagsFor(id, meta)
	if err := os.RemoveAll(pub); err != nil {
		log.Printf("build %s : purge de public/ impossible : %v", id, err)
		return
	}
	pages, err := s.listPages(id)
	if err != nil {
		log.Printf("build %s : listage des pages : %v", id, err)
		return
	}
	built := 0
	for _, p := range pages {
		if ok, err := s.buildPage(id, p, f); err != nil {
			log.Printf("build %s/%s : %v", id, p, err)
		} else if ok {
			built++
		}
	}
	if err := s.writePWAFiles(id, meta, f); err != nil {
		log.Printf("build %s : fichiers PWA : %v", id, err)
	}
	log.Printf("build %s : %d/%d pages publiées (compile=%t fonts=%t pwa=%t gdpr=%t)", id, built, len(pages), f.compile, f.fonts, f.genSW, f.gdpr)
}

// RebuildPage reconstruit une seule page (après un « Enregistrer »).
func (s *Store) RebuildPage(id, rel string) {
	mu := s.buildLock(id)
	mu.Lock()
	defer mu.Unlock()
	if s.srcDir(id) == s.publicDir(id) {
		return
	}
	f := s.flagsFor(id, s.meta(id))
	if _, err := s.buildPage(id, rel, f); err != nil {
		log.Printf("build %s/%s : %v", id, rel, err)
	}
}

// BuildAll reconstruit tous les sites (au démarrage, en goroutine).
func (s *Store) BuildAll() {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			s.BuildSite(e.Name())
		}
	}
}
