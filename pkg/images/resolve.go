package images

import (
	"context"
	"hash/fnv"
	"html"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxResolvePerFragment = 12 // borne le travail (et le quota consommé) par bloc
	resolvePerPage        = 8
	resolveParallel       = 4
	resolveBudget         = 20 * time.Second // filet : un fournisseur lent ne doit pas geler la génération
	avatarMaxSide         = 160
)

// placehold.co/1200x800/… ou placehold.co/400 (carré). La borne finale évite
// de lire un début de couleur hexadécimale (« /1e293b ») comme une taille.
var placeholdDims = regexp.MustCompile(`(?i)placehold\.co/(\d{1,5})(?:x(\d{1,5}))?(?:[/?.@#]|$)`)

// Resolve remplace, dans un fragment HTML, le src des <img> porteurs de
// data-photo par une vraie photo. Retourne le fragment réécrit et le nombre
// d'images remplacées. Ne renvoie jamais d'erreur bloquante : en cas d'échec
// de recherche pour une image, celle-ci garde son placeholder.
//
// Le fragment n'est jamais re-sérialisé : seules les balises <img> modifiées
// changent, tout le reste est recopié octet pour octet.
func (s *Service) Resolve(ctx context.Context, fragment string) (string, int) {
	if !s.Enabled() || ctx.Err() != nil || indexFold(fragment, "data-photo") < 0 {
		return fragment, 0
	}
	imgs := scanPhotoImgs(fragment, maxResolvePerFragment)
	if len(imgs) == 0 {
		return fragment, 0
	}
	ctx, cancel := context.WithTimeout(ctx, resolveBudget)
	defer cancel()
	results := s.searchAll(ctx, imgs)

	var b strings.Builder
	b.Grow(len(fragment) + 256*len(imgs))
	used := make(map[string]bool)
	last, replaced := 0, 0
	for ordinal, img := range imgs {
		// L'ordinal compte toutes les images à photo, résolues ou non : un
		// échec sur l'une ne décale pas le choix des suivantes.
		p, ok := pickPhoto(results[img.key()], img.query, ordinal, used)
		if !ok {
			continue
		}
		b.WriteString(fragment[last:img.tag.start])
		b.WriteString(rewriteImg(fragment, img.tag, p))
		last = img.tag.end
		replaced++
	}
	if replaced == 0 {
		return fragment, 0
	}
	b.WriteString(fragment[last:])
	return b.String(), replaced
}

// searchAll lance une recherche par couple (requête, orientation) distinct, à
// quelques-unes en parallèle : un bloc de 6 images ne doit pas coûter 6
// allers-retours en série. Les échecs sont simplement absents du résultat.
func (s *Service) searchAll(ctx context.Context, imgs []photoImg) map[string][]Photo {
	var keys []photoImg
	seen := make(map[string]bool)
	for _, img := range imgs {
		if !seen[img.key()] {
			seen[img.key()] = true
			keys = append(keys, img)
		}
	}

	var (
		mu      sync.Mutex
		results = make(map[string][]Photo, len(keys))
		stop    atomic.Bool // clé refusée ou quota épuisé : les autres requêtes échoueraient pareil
		wg      sync.WaitGroup
		jobs    = make(chan photoImg)
	)
	for range min(resolveParallel, len(keys)) {
		wg.Go(func() {
			for img := range jobs {
				if stop.Load() || ctx.Err() != nil {
					continue
				}
				photos, err := s.Search(ctx, img.query, img.orientation, resolvePerPage)
				if err != nil {
					if isFatal(err) {
						stop.Store(true)
					}
					continue
				}
				mu.Lock()
				results[img.key()] = photos
				mu.Unlock()
			}
		})
	}
	for _, img := range keys {
		jobs <- img
	}
	close(jobs)
	wg.Wait()
	return results
}

// pickPhoto : choix déterministe mais varié. Le hash de la requête fixe le
// point de départ, l'ordinal le décale d'une image à l'autre, et on avance
// tant que la photo a déjà servi dans ce fragment (s'il reste des alternatives).
func pickPhoto(photos []Photo, query string, ordinal int, used map[string]bool) (Photo, bool) {
	usable := make([]Photo, 0, len(photos))
	for _, p := range photos {
		if p.URL != "" {
			usable = append(usable, p)
		}
	}
	if len(usable) == 0 {
		return Photo{}, false
	}
	h := fnv.New32a()
	h.Write([]byte(query))
	start := int((uint64(h.Sum32()) + uint64(ordinal)) % uint64(len(usable)))
	for i := range usable {
		p := usable[(start+i)%len(usable)]
		if k := p.Provider + ":" + p.ID; !used[k] {
			used[k] = true
			return p, true
		}
	}
	return usable[start], true // tout a déjà servi : on réutilise plutôt que de laisser le placeholder
}

// inferOrientation déduit l'orientation des dimensions du placeholder.
// Les petites images (avatars) sont cherchées en carré : c'est le cadrage
// qui survit le mieux à un rounded-full.
func inferOrientation(src string) string {
	m := placeholdDims.FindStringSubmatch(src)
	if m == nil {
		return ""
	}
	w, _ := strconv.Atoi(m[1])
	h := w // placehold.co/400 ⇒ 400x400
	if m[2] != "" {
		h, _ = strconv.Atoi(m[2])
	}
	if w <= 0 || h <= 0 {
		return ""
	}
	if w <= avatarMaxSide && h <= avatarMaxSide {
		return "square"
	}
	switch ratio := float64(w) / float64(h); {
	case ratio > 1.2:
		return "landscape"
	case ratio < 0.8:
		return "portrait"
	}
	return "square"
}

// ---------- Réécriture d'une balise ----------

type edit struct {
	start, end int
	text       string
}

// rewriteImg renvoie la balise réécrite : src remplacé, crédit posé (ou mis à
// jour si le fragment avait déjà été résolu), loading="lazy" ajouté s'il
// manque. alt n'est jamais touché : l'alt du fournisseur est en anglais et
// décrit la photo, pas le propos de la page.
func rewriteImg(s string, t htmlTag, p Photo) string {
	// html.EscapeString échappe & et les deux types de guillemets : la valeur
	// est sûre quel que soit le délimiteur d'origine de l'attribut.
	src := html.EscapeString(p.URL)
	credit := html.EscapeString(creditFor(p))

	var edits []edit
	if a, ok := t.attr("src"); ok {
		edits = append(edits, a.setValue(src))
	} else {
		edits = append(edits, edit{t.nameEnd, t.nameEnd, ` src="` + src + `"`})
	}
	tail := ""
	if a, ok := t.attr("data-photo-credit"); ok {
		edits = append(edits, a.setValue(credit))
	} else {
		tail += ` data-photo-credit="` + credit + `"`
	}
	if _, ok := t.attr("loading"); !ok {
		tail += ` loading="lazy"`
	}
	if tail != "" {
		// Après le dernier attribut : l'éventuel « /> » final reste intact.
		at := t.attrs[len(t.attrs)-1].end
		edits = append(edits, edit{at, at, tail})
	}
	// Tri stable : à position égale, l'ordre d'ajout (src avant tail) est gardé.
	slices.SortStableFunc(edits, func(a, b edit) int { return a.start - b.start })

	var b strings.Builder
	pos := t.start
	for _, e := range edits {
		b.WriteString(s[pos:e.start])
		b.WriteString(e.text)
		pos = e.end
	}
	b.WriteString(s[pos:t.end])
	return b.String()
}

func creditFor(p Photo) string {
	author := strings.TrimSpace(p.Author)
	if author == "" {
		return p.Provider
	}
	return author + " — " + p.Provider
}

// ---------- Scanner de balises ----------

type htmlAttr struct {
	name             string // en minuscules
	end              int    // juste après l'attribut (guillemet fermant inclus)
	valStart, valEnd int    // valeur brute, sans guillemets ; valStart < 0 ⇒ attribut sans valeur
	quote            byte   // '"', '\'' ou 0 (valeur nue)
}

// setValue produit l'édition qui remplace la valeur par escaped (déjà échappé).
func (a htmlAttr) setValue(escaped string) edit {
	switch {
	case a.valStart < 0: // attribut sans valeur : on lui en greffe une
		return edit{a.end, a.end, `="` + escaped + `"`}
	case a.quote == 0: // valeur nue : elle ne survivrait pas à une URL, on la met entre guillemets
		return edit{a.valStart, a.valEnd, `"` + escaped + `"`}
	}
	return edit{a.valStart, a.valEnd, escaped}
}

type htmlTag struct {
	name       string // en minuscules
	start, end int    // end : juste après « > »
	nameEnd    int
	attrs      []htmlAttr
}

// attr : premier attribut de ce nom — comme le navigateur, qui ignore les doublons.
func (t htmlTag) attr(name string) (htmlAttr, bool) {
	for _, a := range t.attrs {
		if a.name == name {
			return a, true
		}
	}
	return htmlAttr{}, false
}

type photoImg struct {
	tag         htmlTag
	query       string
	orientation string
}

func (p photoImg) key() string { return p.query + "|" + p.orientation }

// scanPhotoImgs relève les <img> porteurs d'un data-photo non vide, dans
// l'ordre du document, jusqu'à limit. Toutes les balises sont analysées (pas
// seulement <img>) pour qu'un « <img » cité dans la valeur d'un attribut, un
// commentaire ou un <script> ne soit pas pris pour une vraie balise.
func scanPhotoImgs(s string, limit int) []photoImg {
	var out []photoImg
	for i := 0; i < len(s) && len(out) < limit; {
		lt := strings.IndexByte(s[i:], '<')
		if lt < 0 {
			break
		}
		i += lt
		if strings.HasPrefix(s[i:], "<!--") {
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				break
			}
			i += 4 + end + 3
			continue
		}
		t, ok := parseTag(s, i)
		if !ok {
			i++
			continue
		}
		i = t.end
		switch t.name {
		case "img":
			a, ok := t.attr("data-photo")
			if !ok || a.valStart < 0 {
				continue
			}
			query := sanitizeQuery(html.UnescapeString(s[a.valStart:a.valEnd]))
			if query == "" {
				continue
			}
			orientation := ""
			if src, ok := t.attr("src"); ok && src.valStart >= 0 {
				val := s[src.valStart:src.valEnd]
				// Image déjà réelle (le modèle renvoie les src existants quand on
				// itère sur un bloc) : on n'y touche plus, sinon la photo
				// changerait à chaque retouche.
				if v := strings.TrimSpace(val); v != "" && indexFold(v, "placehold.co") < 0 {
					continue
				}
				orientation = inferOrientation(val)
			}
			out = append(out, photoImg{tag: t, query: query, orientation: orientation})
		case "script", "style", "textarea":
			// Contenu brut : on saute jusqu'à la balise fermante.
			end := indexFold(s[i:], "</"+t.name)
			if end < 0 {
				return out
			}
			i += end
		}
	}
	return out
}

// parseTag analyse la balise ouvrante qui commence en s[i] == '<', selon les
// règles du tokenizer HTML (simplifiées) : attributs dans n'importe quel ordre,
// guillemets simples ou doubles, valeurs nues, retours à la ligne, « /> ».
// ok == false si ce n'est pas une balise ouvrante ou si elle n'est pas fermée.
func parseTag(s string, i int) (htmlTag, bool) {
	j := i + 1
	if j >= len(s) || !isLetter(s[j]) {
		return htmlTag{}, false
	}
	for j < len(s) && !isSpace(s[j]) && s[j] != '/' && s[j] != '>' {
		j++
	}
	t := htmlTag{name: strings.ToLower(s[i+1 : j]), start: i, nameEnd: j}
	for {
		for j < len(s) && (isSpace(s[j]) || s[j] == '/') {
			j++
		}
		if j >= len(s) {
			return htmlTag{}, false
		}
		if s[j] == '>' {
			t.end = j + 1
			return t, true
		}
		nameStart := j
		if s[j] == '=' { // un « = » en tête fait partie du nom (et garantit qu'on avance)
			j++
		}
		for j < len(s) && !isSpace(s[j]) && s[j] != '/' && s[j] != '>' && s[j] != '=' {
			j++
		}
		a := htmlAttr{name: strings.ToLower(s[nameStart:j]), end: j, valStart: -1, valEnd: -1}
		k := j
		for k < len(s) && isSpace(s[k]) {
			k++
		}
		if k < len(s) && s[k] == '=' {
			k++
			for k < len(s) && isSpace(s[k]) {
				k++
			}
			if k >= len(s) {
				return htmlTag{}, false
			}
			if s[k] == '"' || s[k] == '\'' {
				closing := strings.IndexByte(s[k+1:], s[k])
				if closing < 0 {
					return htmlTag{}, false
				}
				a.quote = s[k]
				a.valStart, a.valEnd = k+1, k+1+closing
				j = a.valEnd + 1
			} else {
				a.valStart = k
				for k < len(s) && !isSpace(s[k]) && s[k] != '>' {
					k++
				}
				a.valEnd = k
				j = k
			}
			a.end = j
		}
		t.attrs = append(t.attrs, a)
	}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

func isLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// indexFold : strings.Index insensible à la casse ASCII ; sub doit être en
// minuscules. On évite strings.ToLower sur tout le fragment, qui peut changer
// la longueur en octets de certains caractères et fausserait les positions.
func indexFold(s, sub string) int {
	n := len(sub)
outer:
	for i := 0; i+n <= len(s); i++ {
		for j := 0; j < n; j++ {
			c := s[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != sub[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}
