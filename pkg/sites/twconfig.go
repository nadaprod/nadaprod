package sites

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Config Tailwind inline des sites importés — lue, jamais exécutée.
//
// Le binaire Tailwind standalone évalue son fichier de config comme du
// JavaScript. Tout ce qui y est écrit s'exécute donc sur le serveur, avec les
// droits du service : le contenu d'une page (modifiable par n'importe quel
// compte auquel le site est attribué) n'a rien à y faire tel quel.
//
// ParseTailwindConfig lit l'affectation « tailwind.config = { … } » d'un
// <script> inline comme un LITTÉRAL et rien d'autre : objets, tableaux,
// chaînes, nombres, booléens, null — avec les tolérances usuelles du JS écrit
// à la main (clés non citées, guillemets simples, virgules finales,
// commentaires). Tout ce qui pourrait être évalué (appel, identifiant,
// fonction, gabarit `…`, spread, clé calculée, seconde instruction…) est
// refusé : la page publiée garde alors son CDN, comme pour toute config
// incompilable. Le résultat est réémis en JSON — le fichier de config passé
// au binaire ne contient plus qu'une donnée.

const (
	maxConfigBytes = 64 << 10 // au-delà, ce n'est pas une config écrite à la main
	maxConfigDepth = 32
)

var errNotLiteral = errors.New("config Tailwind : seul un littéral objet est accepté")

// twValue : arbre de valeurs, l'ordre des clés est conservé (il compte pour
// Tailwind, par exemple l'ordre des points de rupture dans « screens »).
type twValue struct {
	kind byte // 'o' objet, 'a' tableau, 's' chaîne, 'n' nombre, 'b' booléen, 'z' null
	str  string
	num  string // représentation JSON déjà validée
	b    bool
	keys []string
	vals []twValue
}

func (v twValue) writeJSON(sb *strings.Builder) {
	switch v.kind {
	case 'o':
		sb.WriteByte('{')
		for i, k := range v.keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			key, _ := json.Marshal(k)
			sb.Write(key)
			sb.WriteByte(':')
			v.vals[i].writeJSON(sb)
		}
		sb.WriteByte('}')
	case 'a':
		sb.WriteByte('[')
		for i, x := range v.vals {
			if i > 0 {
				sb.WriteByte(',')
			}
			x.writeJSON(sb)
		}
		sb.WriteByte(']')
	case 's':
		s, _ := json.Marshal(v.str)
		sb.Write(s)
	case 'n':
		sb.WriteString(v.num)
	case 'b':
		sb.WriteString(strconv.FormatBool(v.b))
	default:
		sb.WriteString("null")
	}
}

type twParser struct {
	src string
	pos int
}

func (p *twParser) fail(format string, a ...any) error {
	return fmt.Errorf("%w : %s (position %d)", errNotLiteral, fmt.Sprintf(format, a...), p.pos)
}

// skip passe les blancs et les commentaires // et /* */.
func (p *twParser) skip() error {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			p.pos++
		case strings.HasPrefix(p.src[p.pos:], "//"):
			if i := strings.IndexAny(p.src[p.pos:], "\n\r"); i >= 0 {
				p.pos += i
			} else {
				p.pos = len(p.src)
			}
		case strings.HasPrefix(p.src[p.pos:], "/*"):
			i := strings.Index(p.src[p.pos+2:], "*/")
			if i < 0 {
				return p.fail("commentaire non refermé")
			}
			p.pos += i + 4
		default:
			return nil
		}
	}
	return nil
}

func (p *twParser) peek() byte {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return 0
}

func (p *twParser) expect(c byte) error {
	if err := p.skip(); err != nil {
		return err
	}
	if p.peek() != c {
		return p.fail("« %c » attendu", c)
	}
	p.pos++
	return nil
}

func isIdentStart(r rune) bool { return r == '_' || r == '$' || unicode.IsLetter(r) }
func isIdentPart(r rune) bool  { return isIdentStart(r) || unicode.IsDigit(r) }

func (p *twParser) ident() (string, bool) {
	start := p.pos
	for p.pos < len(p.src) {
		r, n := utf8.DecodeRuneInString(p.src[p.pos:])
		if p.pos == start && !isIdentStart(r) || p.pos > start && !isIdentPart(r) {
			break
		}
		p.pos += n
	}
	return p.src[start:p.pos], p.pos > start
}

// str lit une chaîne entre ' ou " avec les échappements JS courants. Les
// gabarits (`) ne sont pas des chaînes : ils peuvent interpoler du code.
func (p *twParser) str() (string, error) {
	q := p.src[p.pos]
	p.pos++
	var sb strings.Builder
	for {
		if p.pos >= len(p.src) {
			return "", p.fail("chaîne non refermée")
		}
		c := p.src[p.pos]
		switch {
		case c == q:
			p.pos++
			return sb.String(), nil
		case c == '\n' || c == '\r':
			return "", p.fail("retour à la ligne dans une chaîne")
		case c == '\\':
			p.pos++
			if p.pos >= len(p.src) {
				return "", p.fail("chaîne non refermée")
			}
			e := p.src[p.pos]
			p.pos++
			switch e {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case 'b':
				sb.WriteByte('\b')
			case 'f':
				sb.WriteByte('\f')
			case 'v':
				sb.WriteByte('\v')
			case '0':
				sb.WriteByte(0)
			case '\\', '\'', '"', '/':
				sb.WriteByte(e)
			case 'x':
				if p.pos+2 > len(p.src) {
					return "", p.fail("échappement \\x incomplet")
				}
				n, err := strconv.ParseUint(p.src[p.pos:p.pos+2], 16, 8)
				if err != nil {
					return "", p.fail("échappement \\x invalide")
				}
				sb.WriteRune(rune(n))
				p.pos += 2
			case 'u':
				if p.pos+4 > len(p.src) {
					return "", p.fail("échappement \\u incomplet")
				}
				n, err := strconv.ParseUint(p.src[p.pos:p.pos+4], 16, 16)
				if err != nil {
					return "", p.fail("échappement \\u invalide")
				}
				p.pos += 4
				r := rune(n)
				// Paire de substituts (\uD83D\uDE00 = 😀) : les deux moitiés forment
				// un seul point de code, comme dans le navigateur. Une moitié
				// isolée devient U+FFFD.
				if utf16.IsSurrogate(r) && r < 0xDC00 && p.pos+6 <= len(p.src) && p.src[p.pos] == '\\' && p.src[p.pos+1] == 'u' {
					if lo, err := strconv.ParseUint(p.src[p.pos+2:p.pos+6], 16, 16); err == nil && lo >= 0xDC00 && lo <= 0xDFFF {
						r = utf16.DecodeRune(r, rune(lo))
						p.pos += 6
					}
				}
				sb.WriteRune(r)
			case '\n':
				// continuation de ligne : rien à écrire
			case '\r':
				// continuation CR ou CRLF (fichiers édités sous Windows)
				if p.pos < len(p.src) && p.src[p.pos] == '\n' {
					p.pos++
				}
			case 0xE2:
				// U+2028 / U+2029 (LS, PS) : continuations de ligne aussi en JS
				if p.pos+2 <= len(p.src) && p.src[p.pos] == 0x80 && (p.src[p.pos+1] == 0xA8 || p.src[p.pos+1] == 0xA9) {
					p.pos += 2
				} else {
					return "", p.fail("échappement inconnu")
				}
			default:
				return "", p.fail("échappement %q inconnu", "\\"+string(e))
			}
		default:
			sb.WriteByte(c)
			p.pos++
		}
	}
}

// number lit un nombre décimal ou hexadécimal (signe éventuel) et le
// normalise en JSON.
func (p *twParser) number() (string, error) {
	start := p.pos
	if p.peek() == '-' || p.peek() == '+' {
		p.pos++
	}
	if strings.HasPrefix(p.src[p.pos:], "0x") || strings.HasPrefix(p.src[p.pos:], "0X") {
		p.pos += 2
		h := p.pos
		for p.pos < len(p.src) && strings.IndexByte("0123456789abcdefABCDEF", p.src[p.pos]) >= 0 {
			p.pos++
		}
		n, err := strconv.ParseInt(p.src[h:p.pos], 16, 64)
		if err != nil {
			return "", p.fail("nombre hexadécimal invalide")
		}
		if p.src[start] == '-' {
			n = -n
		}
		return strconv.FormatInt(n, 10), nil
	}
	for p.pos < len(p.src) && (p.src[p.pos] >= '0' && p.src[p.pos] <= '9' || p.src[p.pos] == '.' || p.src[p.pos] == 'e' || p.src[p.pos] == 'E' ||
		(p.src[p.pos] == '-' || p.src[p.pos] == '+') && (p.src[p.pos-1] == 'e' || p.src[p.pos-1] == 'E')) {
		p.pos++
	}
	lit := strings.TrimPrefix(p.src[start:p.pos], "+")
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		return "", p.fail("nombre invalide « %s »", p.src[start:p.pos])
	}
	// ".5" ou "5." sont du JS valide mais pas du JSON : on renormalise.
	return jsNumber(f), nil
}

// jsNumber reproduit Number::toString de JS : notation décimale pour
// 1e-6 ≤ |f| < 1e21, exponentielle sans zéro de tête dans l'exposant au-delà
// (« 1e+21 », « 1e-7 »). Une clé numérique (zIndex: { 1000000: … }) doit
// donner exactement le nom de propriété que voit le navigateur.
func jsNumber(f float64) string {
	a := math.Abs(f)
	if f == 0 || (a >= 1e-6 && a < 1e21) {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	m, e, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e") // 1.5e+21, 1e-07
	return m + "e" + string(e[0]) + strings.TrimLeft(e[1:], "0")
}

func (p *twParser) value(depth int) (twValue, error) {
	if depth > maxConfigDepth {
		return twValue{}, p.fail("imbrication trop profonde")
	}
	if err := p.skip(); err != nil {
		return twValue{}, err
	}
	c := p.peek()
	switch {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '\'' || c == '"':
		s, err := p.str()
		return twValue{kind: 's', str: s}, err
	case c >= '0' && c <= '9' || c == '-' || c == '+' || c == '.':
		n, err := p.number()
		return twValue{kind: 'n', num: n}, err
	}
	if id, ok := p.ident(); ok {
		switch id {
		case "true", "false":
			return twValue{kind: 'b', b: id == "true"}, nil
		case "null":
			return twValue{kind: 'z'}, nil
		}
		// Tout autre identifiant (variable, fonction, require, undefined…)
		// supposerait une évaluation : refusé.
		return twValue{}, p.fail("identifiant « %s » non admis dans un littéral", id)
	}
	return twValue{}, p.fail("valeur attendue")
}

func (p *twParser) object(depth int) (twValue, error) {
	v := twValue{kind: 'o'}
	p.pos++ // {
	for {
		if err := p.skip(); err != nil {
			return v, err
		}
		if p.peek() == '}' {
			p.pos++
			return v, nil
		}
		var key string
		switch c := p.peek(); {
		case c == '\'' || c == '"':
			k, err := p.str()
			if err != nil {
				return v, err
			}
			key = k
		case c >= '0' && c <= '9':
			n, err := p.number()
			if err != nil {
				return v, err
			}
			key = n
		default:
			id, ok := p.ident()
			if !ok {
				return v, p.fail("clé attendue")
			}
			key = id
		}
		if err := p.expect(':'); err != nil {
			// « get x() », « x() », « [x]: », « ...x » : rien de tout cela n'est un littéral.
			return v, err
		}
		val, err := p.value(depth + 1)
		if err != nil {
			return v, err
		}
		v.keys = append(v.keys, key)
		v.vals = append(v.vals, val)
		if err := p.skip(); err != nil {
			return v, err
		}
		switch p.peek() {
		case ',':
			p.pos++
		case '}':
		default:
			return v, p.fail("« , » ou « } » attendu")
		}
	}
}

func (p *twParser) array(depth int) (twValue, error) {
	v := twValue{kind: 'a'}
	p.pos++ // [
	for {
		if err := p.skip(); err != nil {
			return v, err
		}
		if p.peek() == ']' {
			p.pos++
			return v, nil
		}
		val, err := p.value(depth + 1)
		if err != nil {
			return v, err
		}
		v.vals = append(v.vals, val)
		if err := p.skip(); err != nil {
			return v, err
		}
		switch p.peek() {
		case ',':
			p.pos++
		case ']':
		default:
			return v, p.fail("« , » ou « ] » attendu")
		}
	}
}

// ParseTailwindConfig transforme le corps d'un <script> « tailwind.config =
// { … } » en JSON équivalent. Corps vide ⇒ "{}". Erreur ⇒ le script n'est
// pas un pur littéral (ou est mal formé) : à l'appelant de dégrader.
func ParseTailwindConfig(body string) (string, error) {
	if len(body) > maxConfigBytes {
		return "", fmt.Errorf("%w : script de %d Ko", errNotLiteral, len(body)>>10)
	}
	p := &twParser{src: body}
	if err := p.skip(); err != nil {
		return "", err
	}
	if p.pos == len(p.src) {
		return "{}", nil
	}
	for _, want := range []string{"tailwind", ".", "config", "="} {
		if err := p.skip(); err != nil {
			return "", err
		}
		if want == "=" && strings.HasPrefix(p.src[p.pos:], "==") {
			return "", p.fail("affectation attendue")
		}
		if !strings.HasPrefix(p.src[p.pos:], want) {
			return "", p.fail("« tailwind.config = » attendu")
		}
		p.pos += len(want)
	}
	if err := p.skip(); err != nil {
		return "", err
	}
	if p.peek() != '{' {
		return "", p.fail("littéral objet attendu après « = »")
	}
	v, err := p.object(0)
	if err != nil {
		return "", err
	}
	if err := p.skip(); err != nil {
		return "", err
	}
	if p.peek() == ';' {
		p.pos++
		if err := p.skip(); err != nil {
			return "", err
		}
	}
	if p.pos != len(p.src) {
		// Une seconde instruction pourrait modifier la config après coup : le
		// résultat ne serait plus celui que le navigateur applique. Refusé.
		return "", p.fail("code après la config")
	}
	var sb strings.Builder
	v.writeJSON(&sb)
	return sb.String(), nil
}
