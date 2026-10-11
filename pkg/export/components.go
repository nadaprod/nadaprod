package export

// Conversion d'un bloc (fragment HTML Tailwind produit par le LLM) en composant
// React (.jsx) ou Vue 3 (.vue).
//
// Le fragment est analysé par x/net/html en contexte <body> — balises et attributs
// SVG ressortent donc déjà à la bonne casse (viewBox, clipPath, linearGradient…) —,
// nettoyé (scripts, styles et gestionnaires inline retirés et comptés), puis
// re-sérialisé par un moteur de mise en page commun aux deux dialectes. Ce qui
// change d'un dialecte à l'autre : l'écriture des attributs, des textes, des
// commentaires, et surtout la sémantique des blancs :
//   - JSX supprime tout blanc contenant un saut de ligne → une espace significative
//     en fin de ligne s'écrit {" "} ;
//   - Vue (mode « condense ») garde une espace entre un texte et un élément séparés
//     par un saut de ligne, mais la supprime entre deux éléments → {{ ' ' }} dans ce
//     seul cas.
//
// L'importance d'un blanc est estimée sans CSS : balise en ligne ou non, corrigée par
// les utilitaires Tailwind d'affichage sans variante (block, flex, inline-flex…) ; dans
// un conteneur flex/grid, aucun blanc entre enfants ne compte.
//
// Limites assumées :
//   - un commentaire collé à du texte (sans blanc) gagne une espace côté Vue ;
//   - `!important` est retiré des styles JSX (React ignorerait la déclaration entière) ;
//   - les attributs au nom invalide en JSX (`:class`, `x-on:…`) sont retirés, comme les
//     directives (`v-…`, `:…`, `#…`) côté Vue, où elles changeraient de sens ;
//   - une balise inconnue sans tiret (`<foo>`) reste telle quelle : Vue la prendra pour
//     un composant à résoudre.

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// compMaxWidth : largeur visée pour le remplissage des lignes. Une balise ouvrante
// (longue liste de classes) peut la dépasser : on ne coupe jamais dans une balise.
const compMaxWidth = 100

// Espace significative en fin de ligne, là où le saut de ligne l'effacerait.
const (
	jsxSpace = `{" "}`
	vueSpace = `{{ ' ' }}`
)

type compDialect int

const (
	dialectJSX compDialect = iota
	dialectVue
)

func tagSet(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

var (
	// « display: inline » par défaut : leur contenu coule dans le texte du parent,
	// leurs blancs de bord sont donc significatifs.
	flowInlineTags = tagSet("a", "abbr", "b", "bdi", "bdo", "cite", "code", "data", "del", "dfn",
		"em", "i", "ins", "kbd", "label", "mark", "q", "s", "samp", "small", "span", "strong",
		"sub", "sup", "time", "u", "var")
	// En ligne mais atomiques (remplacés ou inline-block) : les blancs comptent autour,
	// pas à l'intérieur.
	atomicInlineTags = tagSet("audio", "br", "button", "canvas", "embed", "iframe", "img", "input",
		"math", "meter", "object", "output", "picture", "progress", "select", "svg", "textarea",
		"video", "wbr")
	voidTags = tagSet("area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta",
		"param", "source", "track", "wbr")
	booleanAttrs = tagSet("allowfullscreen", "async", "autofocus", "autoplay", "checked", "controls",
		"default", "defer", "disabled", "formnovalidate", "hidden", "inert", "ismap", "itemscope",
		"loop", "multiple", "muted", "nomodule", "novalidate", "open", "playsinline", "readonly",
		"required", "reversed", "selected")
	// <input> dont `value` n'est pas saisi par l'utilisateur : React l'accepte sans onChange.
	fixedValueInputs = tagSet("button", "checkbox", "hidden", "image", "radio", "reset", "submit")
	displayUtils     = tagSet("block", "inline-block", "inline", "flex", "inline-flex", "grid",
		"inline-grid", "hidden", "table", "contents", "flow-root", "list-item")
	flexDisplays = tagSet("flex", "inline-flex", "grid", "inline-grid")
)

// reactAttrNames : attributs HTML (clé en minuscules, telle que livrée par le parseur)
// dont le nom de prop React diffère. Les attributs SVG en kebab-case sont traités par
// règle générale (camelCase) dans jsxAttrName.
var reactAttrNames = map[string]string{
	"accept-charset": "acceptCharset", "accesskey": "accessKey", "allowfullscreen": "allowFullScreen",
	"autocapitalize": "autoCapitalize", "autocomplete": "autoComplete", "autocorrect": "autoCorrect",
	"autofocus": "autoFocus", "autoplay": "autoPlay", "cellpadding": "cellPadding",
	"cellspacing": "cellSpacing", "charset": "charSet", "class": "className", "colspan": "colSpan",
	"contenteditable": "contentEditable", "controlslist": "controlsList", "crossorigin": "crossOrigin",
	"datetime": "dateTime", "disablepictureinpicture": "disablePictureInPicture",
	"disableremoteplayback": "disableRemotePlayback", "enctype": "encType",
	"enterkeyhint": "enterKeyHint", "fetchpriority": "fetchPriority", "for": "htmlFor",
	"formaction": "formAction", "formenctype": "formEncType", "formmethod": "formMethod",
	"formnovalidate": "formNoValidate", "formtarget": "formTarget", "frameborder": "frameBorder",
	"hreflang": "hrefLang", "http-equiv": "httpEquiv", "inputmode": "inputMode", "itemid": "itemID",
	"itemprop": "itemProp", "itemref": "itemRef", "itemscope": "itemScope", "itemtype": "itemType",
	"marginheight": "marginHeight", "marginwidth": "marginWidth", "maxlength": "maxLength",
	"mediagroup": "mediaGroup", "minlength": "minLength", "nomodule": "noModule",
	"novalidate": "noValidate", "playsinline": "playsInline", "popovertarget": "popoverTarget",
	"popovertargetaction": "popoverTargetAction", "radiogroup": "radioGroup", "readonly": "readOnly",
	"referrerpolicy": "referrerPolicy", "rowspan": "rowSpan", "spellcheck": "spellCheck",
	"srcdoc": "srcDoc", "srclang": "srcLang", "srcset": "srcSet", "tabindex": "tabIndex",
	"usemap": "useMap",
}

var (
	// `&` est ré-échappé : JSX comme Vue décodent les entités dans le texte.
	// U+00A0 → &nbsp; pour rester visible et survivre aux éditeurs.
	jsxTextEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;",
		"{", "&#123;", "}", "&#125;", "\u00a0", "&nbsp;")
	// `{{` ouvrirait une interpolation Vue : on l'émet via une interpolation littérale.
	vueTextEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;",
		"{{", "{{ '{{' }}", "\u00a0", "&nbsp;")
)

// ---------- API ----------

// ToJSX convertit un fragment HTML en composant React fonctionnel (fichier .jsx complet).
// name est un identifiant PascalCase déjà valide (ex. "Hero", "Pricing2").
func ToJSX(fragment, name string) (string, error) {
	p, root, items, err := prepareComponent(fragment, name, dialectJSX)
	if err != nil {
		return "", err
	}
	p.write("// Généré par NADAPROD — composant React (classes Tailwind : voir styles.css).\n")
	if note := p.removedNote(); note != "" {
		p.write("// " + note + "\n")
	}
	p.write("\nexport default function " + name + "() {\n  return (")
	if single := singleRoot(items); single != nil {
		// Racine unique : les commentaires qui l'entourent (« <!-- Hero --> », fréquent
		// en sortie de LLM) sont hors JSX, donc de simples commentaires JS — pas besoin
		// d'un fragment pour si peu.
		for _, it := range items {
			p.newline(2)
			if it.kind == itemComment {
				p.write(strings.Trim(p.comment(it.node), "{}"))
			} else {
				p.element(single, 2)
			}
		}
	} else {
		// Plusieurs racines ou texte nu : JSX exige un parent unique.
		p.newline(2)
		p.write("<>")
		p.fill(root, items, 3, false)
		p.newline(2)
		p.write("</>")
	}
	p.newline(1)
	p.write(");\n}\n")
	return p.b.String(), nil
}

// ToVue convertit un fragment HTML en composant monofichier Vue 3 (.vue complet).
// Vue 3 accepte plusieurs racines : aucun enrobage n'est ajouté.
func ToVue(fragment, name string) (string, error) {
	p, root, items, err := prepareComponent(fragment, name, dialectVue)
	if err != nil {
		return "", err
	}
	p.write("<!-- Généré par NADAPROD — composant Vue 3 (classes Tailwind : voir styles.css). -->\n<template>")
	p.fill(root, items, 1, false)
	p.newline(0)
	p.write("</template>\n\n<script setup>\n// Composant " + name + ".\n")
	if note := p.removedNote(); note != "" {
		p.write("// " + note + "\n")
	} else {
		p.write("// Statique pour l'instant : aucun script ni gestionnaire dans le bloc d'origine.\n")
	}
	p.write("</script>\n")
	return p.b.String(), nil
}

// singleRoot renvoie l'unique élément racine, ou nil s'il y en a plusieurs ou du texte nu.
func singleRoot(items []compItem) *html.Node {
	var single *html.Node
	for _, it := range items {
		switch {
		case it.kind == itemComment:
		case it.kind == itemElement && single == nil:
			single = it.node
		default:
			return nil
		}
	}
	return single
}

// ComponentName dérive un nom PascalCase valide d'un type de bloc ("hero" → "Hero",
// "call-to-action" → "CallToAction", "" ou invalide → "Block") ; n > 1 ajoute le suffixe
// numérique ("Hero2").
func ComponentName(blockType string, n int) string {
	var b strings.Builder
	upper := true // tout caractère hors [A-Za-z0-9] sépare les mots
	for _, r := range blockType {
		switch {
		case r >= 'a' && r <= 'z':
			if upper {
				r -= 'a' - 'A'
			}
			b.WriteRune(r)
			upper = false
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			upper = false
		default:
			upper = true
		}
	}
	name := b.String()
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		name = "Block"
	}
	if n > 1 {
		name += strconv.Itoa(n)
	}
	return name
}

// validComponentName : le nom est injecté tel quel dans le code généré — on refuse tout
// ce qui n'est pas un identifiant ASCII commençant par une majuscule (ce qui écarte
// aussi les mots réservés, tous en minuscules).
func validComponentName(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case i > 0 && (c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'):
		default:
			return false
		}
	}
	return name != ""
}

// ---------- Préparation de l'arbre ----------

type inlineResult struct {
	s  string
	ok bool
}

type compPrinter struct {
	d   compDialect
	b   strings.Builder
	col int // colonne courante, en runes
	// Retraits comptés une seule fois par strip, avant tout rendu.
	scripts, styles, handlers int
	inl                       map[*html.Node]inlineResult
	orders                    map[string][]string // cf. sourceAttrOrders
}

func prepareComponent(fragment, name string, d compDialect) (*compPrinter, *html.Node, []compItem, error) {
	if !validComponentName(name) {
		return nil, nil, nil, fmt.Errorf("nom de composant invalide : %q", name)
	}
	fragment = strings.ToValidUTF8(fragment, string(utf8.RuneError))
	ctx := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	// Scripting désactivé : le contenu de <noscript> est analysé comme du HTML normal.
	nodes, err := html.ParseFragmentWithOptions(strings.NewReader(fragment), ctx, html.ParseOptionEnableScripting(false))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fragment HTML illisible : %w", err)
	}
	// Racine virtuelle : les racines du fragment se mettent en page comme n'importe
	// quelle liste d'enfants.
	root := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	p := &compPrinter{d: d, inl: map[*html.Node]inlineResult{}, orders: sourceAttrOrders(fragment)}
	p.strip(root)
	items := collectItems(root)
	for _, it := range items {
		if it.kind != itemComment {
			return p, root, items, nil
		}
	}
	return nil, nil, nil, errors.New("fragment vide : aucun élément à convertir")
}

// strip retire <script>/<style> (HTML ou SVG) et les gestionnaires inline, en les comptant.
func (p *compPrinter) strip(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			switch c.Data {
			case "script":
				p.scripts++
				n.RemoveChild(c)
			case "style":
				p.styles++
				n.RemoveChild(c)
			default:
				p.restoreAttrOrder(c)
				kept := c.Attr[:0]
				for _, a := range c.Attr {
					if isHandlerAttr(a) {
						p.handlers++
						continue
					}
					if isScriptURL(a) {
						// href="javascript:…" : du code inline déguisé, bloqué par React.
						p.handlers++
						a.Val = "#"
					}
					kept = append(kept, a)
				}
				c.Attr = kept
				p.strip(c)
			}
		}
		c = next
	}
}

// formattingTags : éléments « de mise en forme » de l'algorithme HTML. x/net/html trie
// leurs attributs par clé (optimisation de la clause « Noah's Ark ») : l'ordre du source
// y est perdu, on le retrouve par une passe de tokenizer.
var formattingTags = tagSet("a", "b", "big", "code", "em", "font", "i", "nobr", "s", "small",
	"strike", "strong", "tt", "u")

// attrSignature identifie une balise par son nom et son jeu d'attributs, ordre mis à part.
func attrSignature(tag string, attrs []html.Attribute) string {
	parts := make([]string, len(attrs))
	for i, a := range attrs {
		parts[i] = a.Key + "\x00" + a.Val
	}
	sort.Strings(parts)
	return tag + "\x01" + strings.Join(parts, "\x01")
}

// sourceAttrOrders : signature → clés dans l'ordre du source, pour les balises de mise
// en forme à plusieurs attributs. Passer par la signature (plutôt que par la position)
// reste juste même quand le parseur clone ou déplace des éléments mal imbriqués.
func sourceAttrOrders(fragment string) map[string][]string {
	orders := map[string][]string{}
	z := html.NewTokenizer(strings.NewReader(fragment))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return orders
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			if !formattingTags[tok.Data] || len(tok.Attr) < 2 {
				continue
			}
			var attrs []html.Attribute
			var keys []string
			seen := map[string]bool{}
			for _, a := range tok.Attr {
				if !seen[a.Key] { // attribut répété : le parseur garde le premier
					seen[a.Key] = true
					attrs, keys = append(attrs, a), append(keys, a.Key)
				}
			}
			if sig := attrSignature(tok.Data, attrs); orders[sig] == nil {
				orders[sig] = keys
			}
		}
	}
}

func (p *compPrinter) restoreAttrOrder(n *html.Node) {
	if n.Namespace != "" || len(n.Attr) < 2 || !formattingTags[n.Data] {
		return
	}
	keys := p.orders[attrSignature(n.Data, n.Attr)]
	if len(keys) != len(n.Attr) {
		return
	}
	ordered := make([]html.Attribute, 0, len(keys))
	for _, k := range keys {
		for _, a := range n.Attr {
			if a.Key == k {
				ordered = append(ordered, a)
			}
		}
	}
	if len(ordered) == len(n.Attr) {
		n.Attr = ordered
	}
}

// isHandlerAttr : onclick & co, plus les raccourcis Alpine/Vue (@click, x-on:, v-on:).
func isHandlerAttr(a html.Attribute) bool {
	k := a.Key
	if a.Namespace != "" {
		return false
	}
	if strings.HasPrefix(k, "@") || strings.HasPrefix(k, "x-on:") || strings.HasPrefix(k, "v-on:") {
		return true
	}
	if len(k) < 3 || !strings.HasPrefix(k, "on") {
		return false
	}
	for i := 2; i < len(k); i++ {
		if k[i] < 'a' || k[i] > 'z' {
			return false
		}
	}
	return true
}

func isScriptURL(a html.Attribute) bool {
	if a.Key != "href" && a.Key != "action" && a.Key != "formaction" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(a.Val)), "javascript:")
}

// ---------- Classification des nœuds ----------

func nodeAttr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// displayOf lit les utilitaires Tailwind d'affichage : celui sans variante (own) et
// ceux portés par une variante (md:flex, group-hover:block…).
func displayOf(n *html.Node) (own string, variants []string) {
	class, _ := nodeAttr(n, "class")
	for _, tok := range strings.Fields(class) {
		base, prefixed := tok, false
		if !strings.ContainsAny(tok, "[]") {
			if i := strings.LastIndexByte(tok, ':'); i >= 0 {
				base, prefixed = tok[i+1:], true
			}
		}
		base = strings.TrimPrefix(base, "!")
		switch {
		case !displayUtils[base]:
		case prefixed:
			variants = append(variants, base)
		default:
			own = base
		}
	}
	return own, variants
}

// isFlexContainer : flex/grid direct, ou le motif « hidden md:flex » (invisible tant
// qu'il n'est pas flex). Les blancs entre enfants n'y sont jamais rendus.
func isFlexContainer(n *html.Node) bool {
	own, variants := displayOf(n)
	if flexDisplays[own] {
		return true
	}
	if own != "hidden" || len(variants) == 0 {
		return false
	}
	for _, v := range variants {
		if !flexDisplays[v] {
			return false
		}
	}
	return true
}

// inlineLevel : l'élément participe-t-il au flux de texte de son parent ? `hidden`
// ne tranche pas (« hidden sm:inline » est courant) : la balise décide.
func inlineLevel(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch {
	case n.Namespace == "svg" && n.Data != "svg":
		return n.Data == "tspan" || n.Data == "textPath"
	case n.Namespace == "math" && n.Data != "math":
		return false
	}
	switch own, _ := displayOf(n); own {
	case "inline", "inline-block", "inline-flex", "inline-grid":
		return true
	case "block", "flex", "grid", "table", "flow-root", "list-item":
		return false
	}
	if flowInlineTags[n.Data] {
		return !hasBlockChild(n)
	}
	return atomicInlineTags[n.Data]
}

// hasBlockChild : un <a> ou <span> qui enveloppe des blocs (carte cliquable) se met en
// page comme un bloc — les blancs autour de lui ne se voient pas.
func hasBlockChild(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && !inlineLevel(c) {
			return true
		}
	}
	return false
}

// flowInline : « display: inline » pur — les blancs de bord internes sont rendus.
func flowInline(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	if n.Namespace != "" {
		return n.Namespace == "svg" && (n.Data == "tspan" || n.Data == "textPath")
	}
	switch own, _ := displayOf(n); own {
	case "inline":
		return true
	case "", "hidden":
		return flowInlineTags[n.Data] && !hasBlockChild(n)
	}
	return false
}

// preserves : blancs à restituer tels quels (<pre>, <textarea>, utilitaires whitespace-pre*).
func preserves(n *html.Node) bool {
	if n.Namespace == "" && (n.Data == "pre" || n.Data == "textarea") {
		return true
	}
	class, _ := nodeAttr(n, "class")
	for _, tok := range strings.Fields(class) {
		if strings.HasPrefix(tok, "whitespace-pre") || tok == "whitespace-break-spaces" {
			return true
		}
	}
	return false
}

func isBr(n *html.Node) bool {
	return n != nil && n.Type == html.ElementNode && n.Namespace == "" && n.Data == "br"
}

// ---------- Items : enfants normalisés d'un élément ----------

type itemKind int

const (
	itemText itemKind = iota
	itemElement
	itemComment
)

type compItem struct {
	kind itemKind
	text string     // itemText : blancs repliés, bords rognés
	node *html.Node // élément ou commentaire
	// space : un blanc séparait cet item du précédent item non-commentaire.
	space bool
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\f' || c == '\r'
}

// collapseSpace replie chaque suite de blancs ASCII en une espace (U+00A0 n'est pas
// un blanc HTML : il est conservé).
func collapseSpace(s string) string {
	var b strings.Builder
	pending := false
	for i := 0; i < len(s); i++ {
		if isSpaceByte(s[i]) {
			pending = true
			continue
		}
		if pending {
			b.WriteByte(' ')
			pending = false
		}
		b.WriteByte(s[i])
	}
	if pending {
		b.WriteByte(' ')
	}
	return b.String()
}

// edgeSpace : l'élément (display: inline) commence/finit-il par un blanc rendu ? Ce
// blanc est alors « remonté » chez le parent — visuellement équivalent, et l'intérieur
// des éléments peut être rogné sans perte.
func edgeSpace(n *html.Node) (lead, trail bool) {
	if f := n.FirstChild; f != nil {
		if f.Type == html.TextNode {
			lead = f.Data != "" && isSpaceByte(f.Data[0])
		} else if flowInline(f) && !preserves(f) {
			lead, _ = edgeSpace(f)
		}
	}
	if l := n.LastChild; l != nil {
		if l.Type == html.TextNode {
			trail = l.Data != "" && isSpaceByte(l.Data[len(l.Data)-1])
		} else if flowInline(l) && !preserves(l) {
			_, trail = edgeSpace(l)
		}
	}
	return lead, trail
}

func collectItems(parent *html.Node) []compItem {
	var items []compItem
	pending := false // blanc vu depuis le dernier item non-commentaire
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			txt := collapseSpace(c.Data)
			core := strings.Trim(txt, " ")
			if core == "" {
				pending = pending || txt != ""
				continue
			}
			if txt[0] == ' ' {
				pending = true
			}
			// Deux textes adjacents (un <script> retiré entre eux) : on fusionne.
			if n := len(items); n > 0 && items[n-1].kind == itemText {
				if pending {
					items[n-1].text += " "
				}
				items[n-1].text += core
			} else {
				items = append(items, compItem{kind: itemText, text: core, space: pending})
			}
			pending = txt[len(txt)-1] == ' '
		case html.ElementNode:
			lead, trail := false, false
			if flowInline(c) && !preserves(c) {
				lead, trail = edgeSpace(c)
			}
			items = append(items, compItem{kind: itemElement, node: c, space: pending || lead})
			pending = trail
		case html.CommentNode:
			items = append(items, compItem{kind: itemComment, node: c})
		}
	}
	if len(items) > 0 {
		items[0].space = false
	}
	return items
}

func itemInline(it *compItem) bool {
	return it.kind == itemText || (it.kind == itemElement && inlineLevel(it.node))
}

// ---------- Jetons et remplissage des lignes ----------

const (
	boundBreak = iota // saut de ligne imposé, aucun blanc à préserver
	boundSpace        // espace significative : même ligne, ou coupure propre au dialecte
	boundGlue         // collés dans la source : on ne coupe que contraint
)

type compToken struct {
	s     string     // rendu sur une ligne ; vide → élément rendu par element()
	node  *html.Node // élément (s == "")
	text  bool       // mot d'un nœud texte
	bound int        // frontière avec le jeton précédent
	carry bool       // JSX : espace significative à reporter par-dessus un commentaire
}

func runeWidth(s string) int { return utf8.RuneCountInString(s) }

func (p *compPrinter) tokens(parent *html.Node, items []compItem, lvl int) []compToken {
	flex := isFlexContainer(parent)
	var toks []compToken
	var lastReal *compItem // dernier item non-commentaire
	for i := range items {
		it := &items[i]
		bound, carry := boundBreak, false
		if i > 0 {
			prev := &items[i-1]
			switch {
			case it.kind == itemComment:
			case prev.kind == itemComment:
				carry = it.space && !flex && lastReal != nil && itemInline(lastReal) && itemInline(it)
			case flex || !itemInline(prev) || !itemInline(it):
			case isBr(prev.node):
				// Après <br>, le saut de ligne du source rend la même chose.
			case isBr(it.node):
				bound = boundGlue // un blanc avant <br> n'est jamais rendu
			case it.space:
				bound = boundSpace
			default:
				bound = boundGlue
			}
		}
		switch it.kind {
		case itemText:
			for j, w := range strings.Split(it.text, " ") {
				t := compToken{s: p.escapeText(w), text: true, bound: boundSpace}
				if j == 0 {
					t.bound, t.carry = bound, carry
				}
				toks = append(toks, t)
			}
		case itemComment:
			toks = append(toks, compToken{s: p.comment(it.node), bound: bound})
		case itemElement:
			t := compToken{node: it.node, bound: bound, carry: carry}
			if inlineLevel(it.node) {
				// En ligne seulement s'il tient seul sur une ligne ; sinon forme bloc.
				if s, ok := p.inline(it.node); ok && (2*lvl+runeWidth(s) <= compMaxWidth || p.hugs(it.node)) {
					t.s = s
				}
			}
			toks = append(toks, t)
		}
		if it.kind != itemComment {
			lastReal = it
		}
	}
	return toks
}

// breakable : peut-on passer à la ligne entre prev et t sans changer le rendu, et que
// faut-il écrire en fin de ligne pour cela ?
func (p *compPrinter) breakable(prev, t *compToken) (bool, string) {
	textSide := prev.text || t.text
	switch {
	case t.bound == boundGlue:
		// Collés : entre deux éléments le saut de ligne est sans effet (JSX comme Vue) ;
		// contre un texte on reste collé (« </a>, » — et Vue y glisserait une espace).
		return !textSide, ""
	case prev.text && t.text:
		return true, "" // entre deux mots, le saut de ligne vaut déjà une espace
	case p.d == dialectVue && textSide:
		return true, "" // Vue garde l'espace dès qu'un texte borde le saut de ligne
	case p.d == dialectVue:
		return true, vueSpace
	}
	return true, jsxSpace
}

// chainWidth : largeur de toks[i] et de tout ce qui lui reste accroché jusqu'à la
// prochaine coupure possible.
func (p *compPrinter) chainWidth(toks []compToken, i int) int {
	w := runeWidth(toks[i].s)
	for j := i + 1; j < len(toks); j++ {
		t := &toks[j]
		if t.bound == boundBreak || t.s == "" {
			break
		}
		if ok, _ := p.breakable(&toks[j-1], t); ok {
			break
		}
		if t.bound == boundSpace {
			w++
		}
		w += runeWidth(t.s)
	}
	return w
}

// fill écrit les enfants au niveau lvl en remplissant les lignes (mots et éléments en
// ligne), chaque élément de bloc sur ses propres lignes.
func (p *compPrinter) fill(parent *html.Node, items []compItem, lvl int, hugFirst bool) {
	toks := p.tokens(parent, items, lvl)
	for i := range toks {
		t := &toks[i]
		switch {
		case i == 0:
			if !hugFirst {
				p.newline(lvl)
			}
		case t.bound == boundBreak:
			if t.carry && p.d == dialectJSX {
				p.write(jsxSpace) // côté Vue, le texte voisin du commentaire garde déjà l'espace
			}
			p.newline(lvl)
		default:
			prev := &toks[i-1]
			sep := ""
			if t.bound == boundSpace {
				sep = " "
			}
			ok, suffix := p.breakable(prev, t)
			// Un élément en forme bloc s'isole dès que possible ; sinon on ne coupe que
			// lorsque la suite ne tient plus sur la ligne.
			want := prev.s == "" || t.s == "" || p.col+len(sep)+p.chainWidth(toks, i) > compMaxWidth
			if ok && want {
				p.write(suffix)
				p.newline(lvl)
			} else {
				p.write(sep)
			}
		}
		if t.s != "" {
			p.write(t.s)
		} else {
			p.element(t.node, lvl)
		}
	}
}

func (p *compPrinter) write(s string) {
	p.b.WriteString(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		p.col = runeWidth(s[i+1:])
	} else {
		p.col += runeWidth(s)
	}
}

func (p *compPrinter) newline(lvl int) {
	p.b.WriteByte('\n')
	p.b.WriteString(strings.Repeat("  ", lvl))
	p.col = 2 * lvl
}

// ---------- Rendu des éléments ----------

// element écrit n à la position courante : sur une ligne s'il tient, sinon en forme
// bloc (enfants indentés).
func (p *compPrinter) element(n *html.Node, lvl int) {
	items := collectItems(n)
	hug := p.hugs(n)
	if s, ok := p.inline(n); ok && (len(items) == 0 || preserves(n) || hug || p.col+runeWidth(s) <= compMaxWidth) {
		p.write(s)
		return
	}
	p.write(p.openTag(n) + ">")
	p.fill(n, items, lvl+1, hug && items[0].kind == itemText)
	if !hug || items[len(items)-1].kind != itemText {
		p.newline(lvl)
	}
	p.write("</" + tagName(n) + ">")
}

// hugs : Vue conserve une espace au bord d'un texte. Dans un élément « display: inline »
// (lien souligné…), le texte de tête/queue doit donc rester collé aux balises : on garde
// l'élément sur une ligne tant que possible, quitte à dépasser la largeur visée.
func (p *compPrinter) hugs(n *html.Node) bool {
	return p.d == dialectVue && flowInline(n) && !isFlexContainer(n)
}

// inline rend n sur une seule ligne si son contenu s'y prête : texte et éléments en
// ligne uniquement, ni commentaire ni <br> (chaque <br> mérite son saut de ligne).
func (p *compPrinter) inline(n *html.Node) (string, bool) {
	if r, ok := p.inl[n]; ok {
		return r.s, r.ok
	}
	s, ok := p.inlineUncached(n)
	p.inl[n] = inlineResult{s, ok}
	return s, ok
}

func (p *compPrinter) inlineUncached(n *html.Node) (string, bool) {
	if preserves(n) {
		return p.preserved(n), true
	}
	items := collectItems(n)
	if len(items) == 0 {
		return p.emptyTag(n), true
	}
	if len(items) > 1 && isFlexContainer(n) {
		return "", false // enfants d'un flex/grid : un par ligne
	}
	var b strings.Builder
	b.WriteString(p.openTag(n) + ">")
	for i, it := range items {
		if i > 0 && it.space {
			b.WriteByte(' ')
		}
		switch {
		case it.kind == itemText:
			b.WriteString(p.escapeText(it.text))
		case it.kind == itemElement && inlineLevel(it.node) && !isBr(it.node):
			s, ok := p.inline(it.node)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		default:
			return "", false
		}
	}
	b.WriteString("</" + tagName(n) + ">")
	return b.String(), true
}

// emptyTag : élément sans enfant. JSX auto-ferme tout ; côté Vue on reste en HTML
// classique (vide sans fermeture, <div></div>), l'auto-fermeture étant réservée au SVG.
func (p *compPrinter) emptyTag(n *html.Node) string {
	switch {
	case p.d == dialectJSX || n.Namespace != "":
		return p.openTag(n) + " />"
	case voidTags[n.Data]:
		return p.openTag(n) + ">"
	}
	return p.openTag(n) + "></" + tagName(n) + ">"
}

// preserved rend un sous-arbre dont les blancs comptent, sans rien réindenter. En JSX
// les textes passent par des littéraux {"…"} (le texte JSX brut replierait les lignes).
func (p *compPrinter) preserved(n *html.Node) string {
	textarea := n.Namespace == "" && n.Data == "textarea"
	if n.FirstChild == nil || (textarea && p.d == dialectJSX) {
		return p.emptyTag(n) // JSX : le contenu du textarea est passé en defaultValue
	}
	var b strings.Builder
	b.WriteString(p.openTag(n) + ">")
	if f := n.FirstChild; p.d == dialectVue && f.Type == html.TextNode && strings.HasPrefix(f.Data, "\n") &&
		n.Namespace == "" && (n.Data == "pre" || textarea) {
		b.WriteByte('\n') // le parseur HTML avale le premier saut de ligne : on le redouble
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if p.d == dialectJSX {
				b.WriteString("{" + jsString(c.Data) + "}")
			} else {
				b.WriteString(p.escapeText(c.Data))
			}
		case html.ElementNode:
			b.WriteString(p.preserved(c))
		case html.CommentNode:
			b.WriteString(p.comment(c))
		}
	}
	b.WriteString("</" + tagName(n) + ">")
	return b.String()
}

func (p *compPrinter) escapeText(s string) string {
	if p.d == dialectJSX {
		return jsxTextEscaper.Replace(s)
	}
	return vueTextEscaper.Replace(s)
}

func (p *compPrinter) comment(c *html.Node) string {
	txt := strings.Join(strings.Fields(c.Data), " ")
	if p.d == dialectJSX {
		return "{/* " + strings.ReplaceAll(txt, "*/", "* /") + " */}"
	}
	for strings.Contains(txt, "--") {
		txt = strings.ReplaceAll(txt, "--", "- -")
	}
	return "<!-- " + txt + " -->"
}

// tagName : un nom de balise exotique (`<foo@bar>`) casserait la syntaxe → <div>.
func tagName(n *html.Node) string {
	if strings.Contains(n.Data, "_") || !validAttrName(n.Data) {
		return "div"
	}
	return n.Data
}

func (p *compPrinter) openTag(n *html.Node) string {
	var b strings.Builder
	b.WriteByte('<')
	b.WriteString(tagName(n))
	if p.d == dialectJSX {
		jsxAttrs(&b, n)
	} else {
		vueAttrs(&b, n)
	}
	return b.String()
}

// ---------- Attributs JSX ----------

func jsxAttrs(b *strings.Builder, n *html.Node) {
	for _, a := range n.Attr {
		key := a.Key
		if a.Namespace != "" {
			key = a.Namespace + ":" + a.Key // xlink:href, xml:space, xmlns:xlink
		}
		if key == "style" {
			if obj := styleObject(a.Val); obj != "" {
				b.WriteString(" style={" + obj + "}")
			}
			continue
		}
		name := jsxAttrName(n, key)
		if name == "" {
			continue
		}
		if n.Namespace == "" {
			// Bloc statique = champs non contrôlés : l'état initial passe par default*,
			// sinon React fige le champ (value/checked sans onChange).
			switch {
			case n.Data == "input" && key == "checked":
				name = "defaultChecked"
			case n.Data == "input" && key == "value" && !fixedValueInputs[inputType(n)]:
				name = "defaultValue"
			case n.Data == "option" && key == "selected":
				continue // reporté en defaultValue sur le <select> parent
			}
		}
		if booleanAttrs[key] && !(key == "hidden" && strings.EqualFold(a.Val, "until-found")) {
			b.WriteString(" " + name) // présent = vrai, quelle que soit la valeur HTML
			continue
		}
		b.WriteString(" " + name + "=" + jsxValue(a.Val))
	}
	if n.Namespace != "" {
		return
	}
	switch n.Data {
	case "select":
		b.WriteString(selectDefault(n))
	case "textarea":
		if v := textContent(n); v != "" {
			b.WriteString(" defaultValue=" + jsxValue(v))
		}
	}
}

// jsxAttrName renvoie le nom de prop React, ou "" si l'attribut n'est pas exprimable.
func jsxAttrName(n *html.Node, key string) string {
	if name, ok := reactAttrNames[key]; ok {
		return name
	}
	if i := strings.IndexByte(key, ':'); i >= 0 {
		switch key[:i] {
		case "xlink", "xml", "xmlns":
			if rest := key[i+1:]; validAttrName(rest) {
				return key[:i] + upperFirst(rest)
			}
		}
		return ""
	}
	if n.Namespace == "svg" && strings.Contains(key, "-") &&
		!strings.HasPrefix(key, "aria-") && !strings.HasPrefix(key, "data-") {
		key = camelCase(key) // stroke-width → strokeWidth
	}
	if !validAttrName(key) {
		return ""
	}
	return key
}

func isLetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isAlnum(c byte) bool  { return isLetter(c) || isDigit(c) }

// validAttrName : [A-Za-z_][A-Za-z0-9_-]*.
func validAttrName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isLetter(c) && c != '_' && (i == 0 || !(isDigit(c) || c == '-')) {
			return false
		}
	}
	return s != ""
}

func upperFirst(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-('a'-'A')) + s[1:]
}

func camelCase(s string) string {
	parts := strings.Split(s, "-")
	for i := 1; i < len(parts); i++ {
		parts[i] = upperFirst(parts[i])
	}
	return strings.Join(parts, "")
}

func inputType(n *html.Node) string {
	t, _ := nodeAttr(n, "type")
	return strings.ToLower(strings.TrimSpace(t))
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				b.WriteString(c.Data)
			} else {
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

// selectDefault traduit les <option selected> en defaultValue du <select> (tableau si
// `multiple` ; sinon la dernière option sélectionnée l'emporte, comme en HTML).
func selectDefault(sel *html.Node) string {
	var vals []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			if _, selected := nodeAttr(c, "selected"); selected && c.Data == "option" {
				v, ok := nodeAttr(c, "value")
				if !ok {
					v = strings.Trim(collapseSpace(textContent(c)), " ")
				}
				vals = append(vals, v)
			}
			walk(c)
		}
	}
	walk(sel)
	if len(vals) == 0 {
		return ""
	}
	if _, multiple := nodeAttr(sel, "multiple"); multiple {
		for i, v := range vals {
			vals[i] = jsString(v)
		}
		return " defaultValue={[" + strings.Join(vals, ", ") + "]}"
	}
	return " defaultValue=" + jsxValue(vals[len(vals)-1])
}

// jsxValue : "valeur" tant que c'est sans ambiguïté, sinon {"…"} en littéral JS. Un
// attribut JSX entre guillemets ne connaît aucun échappement mais décode les entités :
// une valeur contenant « &amp; » tel quel doit donc aussi passer par le littéral.
func jsxValue(v string) string {
	if strings.ContainsFunc(v, func(r rune) bool { return strings.ContainsRune(`"{}\`, r) || jsUnsafe(r) }) || hasCharRef(v) {
		return "{" + jsString(v) + "}"
	}
	return `"` + v + `"`
}

// hasCharRef : la chaîne contient-elle ce qui ressemble à une entité (&nom; &#12; &#x1f;) ?
func hasCharRef(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '&' {
			continue
		}
		j := i + 1
		if j < len(s) && s[j] == '#' {
			j++
		}
		start := j
		for j < len(s) && isAlnum(s[j]) {
			j++
		}
		if j > start && j < len(s) && s[j] == ';' {
			return true
		}
	}
	return false
}

// jsUnsafe : caractère à écrire en \uXXXX dans un littéral JS (contrôles, NBSP,
// séparateurs de ligne Unicode, UTF-8 invalide).
func jsUnsafe(r rune) bool {
	return r < 0x20 || r == 0x7f || r == 0xa0 || r == 0x2028 || r == 0x2029 || r == utf8.RuneError
}

// jsString : littéral JS entre guillemets doubles. strconv.Quote ne convient pas
// (\a, \x.., \U........ n'existent pas en JS). L'UTF-8 lisible est conservé.
func jsString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if jsUnsafe(r) {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ---------- style="…" → objet JS ----------

// styleObject convertit une liste de déclarations CSS en `{ clé: "valeur", … }` ;
// "" si rien d'exploitable. Une propriété répétée garde sa place et prend la dernière
// valeur (comme la cascade, et sans clé dupliquée dans l'objet).
func styleObject(css string) string {
	var keys, vals []string
	index := map[string]int{}
	for _, d := range splitDecls(css) {
		i := strings.IndexByte(d, ':')
		if i <= 0 {
			continue
		}
		key := styleKey(strings.TrimSpace(d[:i]))
		val := strings.Join(strings.Fields(d[i+1:]), " ")
		// React ignore toute déclaration portant !important : on garde la valeur nue.
		if j := strings.LastIndexByte(val, '!'); j >= 0 && strings.EqualFold(strings.TrimSpace(val[j+1:]), "important") {
			val = strings.TrimSpace(val[:j])
		}
		if key == "" || val == "" {
			continue
		}
		if j, seen := index[key]; seen {
			vals[j] = val
			continue
		}
		index[key] = len(keys)
		keys, vals = append(keys, key), append(vals, val)
	}
	if len(keys) == 0 {
		return ""
	}
	parts := make([]string, len(keys))
	for i := range keys {
		parts[i] = keys[i] + ": " + jsString(vals[i])
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// splitDecls coupe sur les `;` hors parenthèses et hors chaînes (url("data:…;base64,…")).
func splitDecls(css string) []string {
	var out []string
	var quote byte
	depth, start := 0, 0
	for i := 0; i < len(css); i++ {
		c := css[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			depth++
		case c == ')' && depth > 0:
			depth--
		case c == ';' && depth == 0:
			out = append(out, css[start:i])
			start = i + 1
		}
	}
	return append(out, css[start:])
}

// styleKey : background-color → backgroundColor ; -webkit-foo → WebkitFoo (mais -ms-foo →
// msFoo, exception React) ; --x reste tel quel, en clé entre guillemets.
func styleKey(prop string) string {
	if strings.HasPrefix(prop, "--") {
		if len(prop) == 2 || strings.ContainsAny(prop, " \t\n\f\r") {
			return ""
		}
		return jsString(prop)
	}
	prop = strings.ToLower(prop)
	for i := 0; i < len(prop); i++ {
		c := prop[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return ""
		}
	}
	parts := strings.Split(prop, "-")
	if parts[0] == "" { // préfixe constructeur
		parts = parts[1:]
		if len(parts) < 2 || parts[0] == "" {
			return ""
		}
		if parts[0] != "ms" {
			parts[0] = upperFirst(parts[0])
		}
	}
	key := parts[0]
	for _, part := range parts[1:] {
		key += upperFirst(part)
	}
	if key == "" || (key[0] >= '0' && key[0] <= '9') {
		return ""
	}
	return key
}

// ---------- Attributs Vue ----------

// vueAttrs : attributs HTML inchangés (class, for, stroke-width…). On écarte les noms
// qui ne se ré-écrivent pas en HTML et ceux que Vue prendrait pour des directives.
func vueAttrs(b *strings.Builder, n *html.Node) {
	for _, a := range n.Attr {
		key := a.Key
		if a.Namespace != "" {
			key = a.Namespace + ":" + a.Key
		}
		if key == "" || strings.ContainsAny(key, " \t\n\f\r\"'<>/=`") || strings.ContainsAny(key[:1], ":@#") ||
			strings.HasPrefix(key, "v-") {
			continue
		}
		if a.Val == "" && booleanAttrs[key] {
			b.WriteString(" " + key)
			continue
		}
		b.WriteString(" " + key + `="` + vueValue(a.Val) + `"`)
	}
}

// vueValue échappe le strict nécessaire : `"`, U+00A0 et les `&` qui formeraient une
// entité — « [&>li]:mt-2 » reste lisible.
func vueValue(v string) string {
	var b strings.Builder
	for i, r := range v {
		switch {
		case r == '"':
			b.WriteString("&quot;")
		case r == 0xa0:
			b.WriteString("&nbsp;")
		case r == '&' && i+1 < len(v) && (v[i+1] == '#' || isAlnum(v[i+1])):
			b.WriteString("&amp;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ---------- Note sur les retraits ----------

// removedNote décrit ce que strip a retiré ("" si rien) — ligne de commentaire placée
// en tête du .jsx ou dans le <script setup>.
func (p *compPrinter) removedNote() string {
	// Côté Vue, pas de « <script> » littéral dans le bloc <script setup> : certains
	// outils découpent les SFC à la regex.
	tag := func(n int, name string) string {
		switch {
		case p.d == dialectJSX:
			return strconv.Itoa(n) + " <" + name + ">"
		case n == 1:
			return "1 balise " + name
		}
		return strconv.Itoa(n) + " balises " + name
	}
	hint := "à réimplémenter avec useState/useEffect"
	if p.d == dialectVue {
		hint = "à réimplémenter avec ref()/onMounted()"
	}
	var parts, hints []string
	if p.scripts > 0 {
		parts = append(parts, tag(p.scripts, "script"))
	}
	if p.styles > 0 {
		parts = append(parts, tag(p.styles, "style"))
	}
	if p.handlers == 1 {
		parts = append(parts, "1 gestionnaire inline")
	} else if p.handlers > 1 {
		parts = append(parts, strconv.Itoa(p.handlers)+" gestionnaires inline")
	}
	if len(parts) == 0 {
		return ""
	}
	if p.scripts+p.handlers > 0 {
		hints = append(hints, hint)
	}
	if p.styles > 0 {
		hints = append(hints, "CSS à reporter dans styles.css")
	}
	list := parts[0]
	if n := len(parts); n > 1 {
		list = strings.Join(parts[:n-1], ", ") + " et " + parts[n-1]
	}
	verb := "retiré"
	if p.scripts+p.styles+p.handlers > 1 {
		verb = "retirés"
	}
	return "NADAPROD — " + list + " " + verb + " : " + strings.Join(hints, " ; ") + "."
}
