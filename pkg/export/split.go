package export

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// Passerelle avec les sites importés.
//
//   - PageCDN : le document « source » d'une page de site (dev/) tel que le
//     studio le dépose — Tailwind via CDN + config du thème inline, comme
//     n'importe quelle page importée : le pipeline de publication compile
//     ensuite, localise les polices, etc.
//   - SplitPage : l'inverse — une page existante découpée en blocs pour
//     l'ouvrir dans le studio.

// PageCDN assemble la page pour dev/ : SEO, favicon, polices (lien Google,
// que le pipeline localisera), CDN + config, blocs.
func PageCDN(p Page) []byte {
	p.normalize()
	return p.document(docOptions{cdn: true, fontLinks: true, bodies: p.bodies()})
}

// Tags dont un enfant direct de <body> hérite le type de bloc ; tout le
// reste devient une « section libre ».
var blockTypeByTag = map[string]string{"header": "header", "nav": "header", "footer": "footer"}

// SplitPage découpe une page en blocs : chaque enfant élément de <body>
// devient un bloc (les <script> de fin de page sont rattachés au bloc qui
// les précède, les <style>/<link>/<meta> de tête sont ignorés — le studio ne
// rend que du Tailwind). Renvoie aussi titre, description et langue.
func SplitPage(raw []byte) (blocks []Block, title, description, lang string) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, "", "", ""
	}
	var body *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "html":
				lang, _ = nodeAttr(n, "lang")
			case "title":
				if n.FirstChild != nil && title == "" {
					title = strings.TrimSpace(n.FirstChild.Data)
				}
			case "meta":
				if name, _ := nodeAttr(n, "name"); strings.ToLower(name) == "description" && description == "" {
					content, _ := nodeAttr(n, "content")
					description = strings.TrimSpace(content)
				}
			case "body":
				body = n
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if body == nil {
		return nil, title, description, lang
	}
	render := func(n *html.Node) string {
		var sb strings.Builder
		html.Render(&sb, n)
		return sb.String()
	}
	pending := "" // scripts/texte à rattacher au bloc précédent
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		switch {
		case c.Type == html.ElementNode && (c.Data == "script" || c.Data == "style" || c.Data == "link"):
			if len(blocks) > 0 && c.Data == "script" {
				blocks[len(blocks)-1].HTML += "\n" + render(c)
			}
		case c.Type == html.ElementNode:
			t := blockTypeByTag[c.Data]
			if t == "" {
				t = "section"
			}
			blocks = append(blocks, Block{Type: t, HTML: pending + render(c)})
			pending = ""
		case c.Type == html.TextNode && strings.TrimSpace(c.Data) != "":
			pending += c.Data // texte nu entre deux blocs : conservé avec le suivant
		}
	}
	if pending != "" && len(blocks) > 0 {
		blocks[len(blocks)-1].HTML += pending
	}
	return blocks, title, description, lang
}
