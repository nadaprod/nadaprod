// Package i18n : segments traduisibles d'un fragment HTML et leur
// réapplication. Les traductions sont indexées par le texte source (Key), pas
// par le balisage : régénérer un bloc ne peut rien casser — un texte inchangé
// retrouve sa traduction, un texte nouveau apparaît manquant. Voir doc/I18N.md.
package i18n

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// LangRe : code de langue accepté (fr, en, pt-br, zh-hant…), en minuscules.
var LangRe = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})?$`)

// Segment : une unité traduisible.
type Segment struct {
	Hash string `json:"hash"`           // Key(Src)
	Src  string `json:"src"`            // texte source normalisé, marqueurs <n>…</n> / <n/>
	Attr string `json:"attr,omitempty"` // "" = contenu ; sinon l'attribut (alt, title…)
}

// Key : identifiant stable d'un texte source (16 hex de SHA-256, espaces repliés).
func Key(src string) string {
	sum := sha256.Sum256([]byte(collapse(src)))
	return hex.EncodeToString(sum[:8])
}

var (
	// Éléments jamais parcourus : ni texte ni attribut à traduire.
	skipped = set(atom.Script, atom.Style, atom.Noscript, atom.Template, atom.Pre, atom.Textarea,
		atom.Iframe, atom.Object, atom.Canvas)
	// Éléments en ligne dont le contenu se traduit avec la phrase qui les entoure.
	inline = set(atom.A, atom.Abbr, atom.B, atom.Bdi, atom.Bdo, atom.Cite, atom.Data, atom.Dfn, atom.Em,
		atom.I, atom.Mark, atom.Q, atom.S, atom.Small, atom.Span, atom.Strong, atom.Sub, atom.Sup,
		atom.Time, atom.U, atom.Del, atom.Ins, atom.Label, atom.Font)
	// Éléments en ligne opaques : gardés tels quels, un seul marqueur <n/>.
	opaque = set(atom.Br, atom.Wbr, atom.Img, atom.Svg, atom.Math, atom.Code, atom.Kbd, atom.Samp,
		atom.Var, atom.Input, atom.Picture)
	// Attributs lus par un humain.
	textAttrs = []string{"alt", "title", "placeholder", "aria-label"}
)

func set(as ...atom.Atom) map[atom.Atom]bool {
	m := make(map[atom.Atom]bool, len(as))
	for _, a := range as {
		m[a] = true
	}
	return m
}

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// noTranslate : translate="no" ou classe notranslate (convention Google/Chrome).
func noTranslate(n *html.Node) bool {
	if v, _ := attr(n, "translate"); strings.EqualFold(v, "no") {
		return true
	}
	c, _ := attr(n, "class")
	return strings.Contains(" "+c+" ", " notranslate ")
}

func isOpaque(n *html.Node) bool {
	return n.Type == html.ElementNode && (opaque[n.DataAtom] || n.Namespace != "" ||
		(inline[n.DataAtom] && (noTranslate(n) || !hasText(n))))
}

// hasText : un nœud texte non blanc dans le sous-arbre.
func hasText(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && collapse(c.Data) != "" || c.Type == html.ElementNode && hasText(c) {
			return true
		}
	}
	return false
}

// inRun : nœud qui peut appartenir à une phrase (texte, commentaire, en ligne).
func inRun(n *html.Node) bool {
	switch n.Type {
	case html.TextNode, html.CommentNode:
		return true
	case html.ElementNode:
		if isOpaque(n) {
			return true
		}
		if !inline[n.DataAtom] {
			return false
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if !inRun(c) {
				return false
			}
		}
		return true
	}
	return false
}

// ---------- Texte normalisé ----------

// collapse replie les blancs HTML (pas l'espace insécable, qui a un sens).
func collapse(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f'
	}), " ")
}

func isHTMLSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' }

var textEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func hasLetter(s string) bool { return strings.IndexFunc(s, unicode.IsLetter) >= 0 }

// run : une phrase — suite de nœuds frères (texte + en ligne) — et ses
// éléments numérotés (marqueur n ⇔ tags[n-1]).
type run struct {
	nodes []*html.Node
	tags  []*html.Node
	src   string
}

func newRun(nodes []*html.Node) *run {
	r := &run{nodes: nodes}
	var b, text strings.Builder
	var ser func(n *html.Node)
	ser = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			b.WriteString(textEsc.Replace(n.Data))
			text.WriteString(n.Data)
		case n.Type != html.ElementNode:
		case isOpaque(n):
			r.tags = append(r.tags, n)
			b.WriteString("<" + strconv.Itoa(len(r.tags)) + "/>")
		default:
			r.tags = append(r.tags, n)
			k := strconv.Itoa(len(r.tags))
			b.WriteString("<" + k + ">")
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				ser(c)
			}
			b.WriteString("</" + k + ">")
		}
	}
	for _, n := range nodes {
		ser(n)
	}
	if hasLetter(text.String()) {
		r.src = collapse(b.String())
	}
	return r
}

// ---------- Parcours ----------

// visitor : appelé pour chaque phrase et chaque attribut traduisible.
type visitor struct {
	onRun  func(*run)
	onAttr func(n *html.Node, i int, src string)
}

func (v *visitor) attrs(n *html.Node) {
	for i, a := range n.Attr {
		translatable := a.Namespace == "" && (contains(textAttrs, a.Key) ||
			(a.Key == "value" && n.DataAtom == atom.Input && isButtonInput(n)))
		if src := collapse(a.Val); translatable && hasLetter(src) {
			v.onAttr(n, i, src)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func isButtonInput(n *html.Node) bool {
	t, _ := attr(n, "type")
	t = strings.ToLower(t)
	return t == "button" || t == "submit" || t == "reset"
}

// subtree : attributs de tous les éléments d'une phrase (alt d'une image en
// ligne, title d'un lien…), sauf sous un élément non traduit.
func (v *visitor) subtree(n *html.Node) {
	if n.Type != html.ElementNode || skipped[n.DataAtom] || noTranslate(n) {
		return
	}
	v.attrs(n)
	if n.DataAtom == atom.Svg || n.Namespace != "" {
		return
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		v.subtree(c)
	}
}

func (v *visitor) visit(n *html.Node) {
	if n.Type == html.ElementNode {
		if skipped[n.DataAtom] || noTranslate(n) || n.Namespace != "" {
			return
		}
		v.attrs(n)
	}
	var nodes []*html.Node
	flush := func() {
		core, edges := trim(nodes)
		for _, m := range edges {
			v.subtree(m) // icônes, <br>… de bord : restent en place
		}
		switch {
		case len(core) == 0:
		case !directText(core):
			// Pas de texte entre les éléments (<span>Libellé</span> <span>Valeur</span>,
			// liens de menu côte à côte) : chacun est traité pour lui-même.
			for _, m := range core {
				if m.Type == html.ElementNode && !isOpaque(m) {
					v.visit(m)
				} else {
					v.subtree(m)
				}
			}
		default:
			// Attributs d'abord : une phrase remplacée est reconstruite à
			// partir de ses éléments d'origine, déjà traduits.
			r := newRun(core)
			for _, m := range core {
				v.subtree(m)
			}
			if r.src != "" {
				v.onRun(r)
			}
		}
		nodes = nil
	}
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling // onRun peut remplacer les nœuds de la phrase
		if inRun(c) {
			nodes = append(nodes, c)
		} else {
			flush()
			v.visit(c)
		}
		c = next
	}
	flush()
}

// trim : sépare le cœur d'une phrase de ses bords inertes (blancs,
// commentaires, opaques) — une icône en tête de bouton n'a rien à traduire.
func trim(nodes []*html.Node) (core, edges []*html.Node) {
	inert := func(n *html.Node) bool {
		return n.Type == html.CommentNode || isOpaque(n) || n.Type == html.TextNode && collapse(n.Data) == ""
	}
	i, j := 0, len(nodes)
	for i < j && inert(nodes[i]) {
		i++
	}
	for j > i && inert(nodes[j-1]) {
		j--
	}
	return nodes[i:j], append(append([]*html.Node{}, nodes[:i]...), nodes[j:]...)
}

// directText : du texte (avec au moins une lettre) entre les éléments.
func directText(nodes []*html.Node) bool {
	for _, n := range nodes {
		if n.Type == html.TextNode && hasLetter(n.Data) {
			return true
		}
	}
	return false
}

// parse : le fragment sous un <body> virtuel.
func parse(fragment string) (*html.Node, error) {
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(fragment), body)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		body.AppendChild(n)
	}
	return body, nil
}

// Extract : les segments d'un fragment, dans l'ordre du document, sans doublon.
func Extract(fragment string) []Segment {
	out := []Segment{}
	body, err := parse(fragment)
	if err != nil {
		return out
	}
	seen := map[string]bool{}
	add := func(src, attr string) {
		if k := Key(src); !seen[k] {
			seen[k] = true
			out = append(out, Segment{Hash: k, Src: src, Attr: attr})
		}
	}
	v := &visitor{
		onRun:  func(r *run) { add(r.src, "") },
		onAttr: func(n *html.Node, i int, src string) { add(src, n.Attr[i].Key) },
	}
	v.visit(body)
	return out
}

// Apply : le fragment traduit. lookup reçoit le texte source (marqueurs
// compris) ; une traduction absente ou aux marqueurs altérés laisse la source
// en place et figure dans missing (sans doublon).
func Apply(fragment string, lookup func(src string) (string, bool)) (string, []string) {
	missing := []string{}
	body, err := parse(fragment)
	if err != nil {
		return fragment, missing
	}
	seen := map[string]bool{}
	tr := func(src string) (string, bool) {
		if dst, ok := lookup(src); ok && Valid(src, dst) {
			return dst, true
		}
		if !seen[src] {
			seen[src] = true
			missing = append(missing, src)
		}
		return "", false
	}
	v := &visitor{
		onAttr: func(n *html.Node, i int, src string) {
			if dst, ok := tr(src); ok {
				n.Attr[i].Val = dst
			}
		},
	}
	v.onRun = func(r *run) {
		if dst, ok := tr(r.src); ok {
			r.replace(dst)
		}
	}
	v.visit(body)

	var b strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			return fragment, missing
		}
	}
	return b.String(), missing
}

// ---------- Marqueurs ----------

var markerRe = regexp.MustCompile(`<(/?)(\d+)(/?)>`)

type marker struct {
	kind byte // 'o' ouvrant, 'c' fermant, 'v' vide
	n    int
}

func markers(s string) []marker {
	var out []marker
	for _, m := range markerRe.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[2])
		switch {
		case m[1] == "/" && m[3] == "":
			out = append(out, marker{'c', n})
		case m[1] == "" && m[3] == "/":
			out = append(out, marker{'v', n})
		case m[1] == "":
			out = append(out, marker{'o', n})
		default:
			out = append(out, marker{'?', n}) // </3/> : invalide
		}
	}
	return out
}

// Valid : dst garde exactement les marqueurs de src (ordre libre), bien imbriqués.
func Valid(src, dst string) bool {
	if strings.TrimSpace(dst) == "" {
		return false
	}
	want := map[marker]int{}
	for _, m := range markers(src) {
		want[m]++
	}
	var stack []int
	for _, m := range markers(dst) {
		switch m.kind {
		case 'o':
			stack = append(stack, m.n)
		case 'c':
			if len(stack) == 0 || stack[len(stack)-1] != m.n {
				return false
			}
			stack = stack[:len(stack)-1]
		case '?':
			return false
		}
		if want[m]--; want[m] < 0 {
			return false
		}
	}
	if len(stack) > 0 {
		return false
	}
	for _, c := range want {
		if c != 0 {
			return false
		}
	}
	return true
}

// replace : remplace les nœuds de la phrase par la traduction (déjà validée),
// en gardant les blancs de bord et les éléments d'origine (attributs, sous-arbre
// des opaques).
func (r *run) replace(dst string) {
	parent := r.nodes[0].Parent
	anchor := r.nodes[len(r.nodes)-1].NextSibling
	lead, trail := edgeSpace(r.nodes)
	for _, n := range r.nodes {
		parent.RemoveChild(n)
	}
	insert := func(n *html.Node) { parent.InsertBefore(n, anchor) }
	if lead != "" {
		insert(&html.Node{Type: html.TextNode, Data: lead})
	}

	stack := []*html.Node{{Type: html.DocumentNode}} // conteneur provisoire
	text := func(s string) {
		if s != "" {
			stack[len(stack)-1].AppendChild(&html.Node{Type: html.TextNode, Data: html.UnescapeString(s)})
		}
	}
	last := 0
	for _, loc := range markerRe.FindAllStringSubmatchIndex(dst, -1) {
		text(dst[last:loc[0]])
		last = loc[1]
		n, _ := strconv.Atoi(dst[loc[4]:loc[5]])
		orig := r.tags[n-1]
		top := stack[len(stack)-1]
		switch {
		case loc[3] > loc[2]: // fermant
			stack = stack[:len(stack)-1]
		case loc[7] > loc[6]: // vide : l'élément d'origine, entier
			top.AppendChild(cloneDeep(orig))
		default:
			el := &html.Node{Type: html.ElementNode, Data: orig.Data, DataAtom: orig.DataAtom, Namespace: orig.Namespace,
				Attr: append([]html.Attribute(nil), orig.Attr...)}
			top.AppendChild(el)
			stack = append(stack, el)
		}
	}
	text(dst[last:])
	for c := stack[0].FirstChild; c != nil; {
		next := c.NextSibling
		stack[0].RemoveChild(c)
		insert(c)
		c = next
	}
	if trail != "" {
		insert(&html.Node{Type: html.TextNode, Data: trail})
	}
}

// edgeSpace : blancs en tête du premier nœud texte et en fin du dernier.
func edgeSpace(nodes []*html.Node) (lead, trail string) {
	if n := nodes[0]; n.Type == html.TextNode {
		i := 0
		for i < len(n.Data) && isHTMLSpace(n.Data[i]) {
			i++
		}
		lead = n.Data[:i]
	}
	if n := nodes[len(nodes)-1]; n.Type == html.TextNode {
		i := len(n.Data)
		for i > 0 && isHTMLSpace(n.Data[i-1]) {
			i--
		}
		trail = n.Data[i:]
	}
	return lead, trail
}

func cloneDeep(n *html.Node) *html.Node {
	c := &html.Node{Type: n.Type, Data: n.Data, DataAtom: n.DataAtom, Namespace: n.Namespace,
		Attr: append([]html.Attribute(nil), n.Attr...)}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.AppendChild(cloneDeep(ch))
	}
	return c
}
