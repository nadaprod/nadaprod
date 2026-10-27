"""Trait de marqueur pour les filets des pages publiques (landing, /debate).

Une image SVG carrée porte les quatre bords : chaque bord est une bande
irrégulière (épaisseur et dérive qui varient, deux reprises d'encre plus
claires), comme un trait tiré au marqueur. Utilisée en `border-image` avec une
découpe de 12 px : n'importe quelle combinaison de bords (haut, gauche…) se
dessine avec la même image, la couleur suit le thème (variables par thème).

    python3 tools/marker/gen.py > web/static/marker.css   (lu par les pages publiques ET l'app)
"""
import math, random, urllib.parse

S, B = 600, 12  # taille de l'image, épaisseur d'une bande (découpe border-image)

def band(rng, length, along):
    """Contour d'une bande de bord (u le long, v en travers), périodique : le
    segment se répète sans couture (border-image « round »)."""
    n = 48
    freqs = [(1, rng.uniform(0, 6.28)), (3, rng.uniform(0, 6.28)), (7, rng.uniform(0, 6.28)), (13, rng.uniform(0, 6.28))]
    def wave(t, amps):
        return sum(a * math.sin(6.28318 * k * t + ph) for (k, ph), a in zip(freqs, amps))
    grit = [rng.uniform(-.8, .8) for _ in range(n)]
    grit.append(grit[0])  # bords râpeux, mais raccord parfait d'un segment à l'autre
    top, bot = [], []
    for i in range(n + 1):
        t = i / n
        u = B + t * (length - 2 * B)
        centre = B / 2 + wave(t, (.55, .35, .2, .1))
        half = 4.7 + wave(t, (.35, .45, .25, .15))  # ≈ 9,4 px sur 12 : un trait gras
        top.append((u, centre - half + grit[i] * .6))
        bot.append((u, centre + half + grit[(i + 7) % n] * .6))
    return top + bot[::-1]

def path(pts, along):
    xy = [(u, v) if along == 'h' else (v, u) for u, v in pts]
    return 'M' + ' L'.join(f'{x:.1f} {y:.1f}' for x, y in xy) + 'Z'

def svg(colour, alpha, seed):
    rng = random.Random(seed)
    parts = []
    for side in ('top', 'bottom', 'left', 'right'):
        along = 'h' if side in ('top', 'bottom') else 'v'
        pts = band(rng, S, along)
        if side == 'bottom':
            pts = [(u, S - B + v) for u, v in pts]
        if side == 'right':
            pts = [(u, S - B + v) for u, v in pts]
        parts.append(f'<path d="{path(pts, along)}"/>')
        # reprises d'encre sèche : quelques éclats courts et clairsemés, jamais une ligne continue
        for k in range(5):
            a = rng.uniform(.03, .9)
            b = a + rng.uniform(.02, .07)
            off = rng.uniform(3.5, 8.5)
            seg = [(B + a * (S - 2 * B), off), (B + b * (S - 2 * B), off + rng.uniform(-.8, .8))]
            if side in ('bottom', 'right'):
                seg = [(u, S - B + v) for u, v in seg]
            (x1, y1), (x2, y2) = [(u, v) if along == 'h' else (v, u) for u, v in seg]
            parts.append(f'<path d="M{x1:.1f} {y1:.1f} L{x2:.1f} {y2:.1f}" stroke="#{"fff" if colour == "0b0b0a" else "000"}" stroke-opacity=".16" stroke-width="{rng.uniform(.6, 1.3):.1f}" stroke-linecap="round" fill="none"/>')
    body = ''.join(parts)
    s = f'<svg xmlns="http://www.w3.org/2000/svg" width="{S}" height="{S}" viewBox="0 0 {S} {S}"><g fill="#{colour}" fill-opacity="{alpha}">{body}</g></svg>'
    return 'url("data:image/svg+xml,' + urllib.parse.quote(s, safe=' =:/",.') .replace('"', "'") + '")'

light, dark = '0b0b0a', 'f3f3ee'
print('/* ── Traits au marqueur (générés par tools/marker/gen.py — ne pas éditer à la main) ── */')
print(':root {')
print(f'  --mk: {svg(light, .94, 7)};')
print(f'  --mk-soft: {svg(light, .72, 11)};')
print('}')
for sel in ('@media (prefers-color-scheme: dark) { :root:not([data-theme="light"])', 'html[data-theme="dark"]'):
    close = ' }' if sel.startswith('@media') else ''
    print(f'{sel} {{ --mk: {svg(dark, .92, 7)}; --mk-soft: {svg(dark, .7, 11)}; }}{close}')
