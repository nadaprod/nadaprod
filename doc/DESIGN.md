# Direction artistique — « l'imprimé »

Un punk qui a fait les Beaux-Arts : il sait ce qu'*épuré* veut dire. Un seul
geste fort par écran, tout le reste tient par la grille, les filets et la
typographie. Landing, /debate et l'app parlent la même langue ; l'app est plus
dense, le landing respire plus, c'est tout.

**L'image** : de l'encre sur du papier chaud (clair) ou de la craie sur du noir
chaud (sombre), et un **surligneur** vert acide. Rien d'autre.

## Les règles

1. **Le vert est un surligneur, pas une couleur de décor.** Il marque : l'action
   principale de l'écran (une seule), l'état courant (onglet actif, élément
   sélectionné), le focus, la sélection de texte. Jamais pour décorer, jamais en
   aplat de carte, jamais six fois sur une même ligne. En texte sur fond clair :
   seulement la version olive (`--acc-fg`), pour un lien ou un état actif.

2. **Quatre voix, chacune son rôle.**
   - *Permanent Marker* : les titres de page, le héros, les grands chiffres — le trait de marqueur, écrit à la main. C'est tout. Une seule graisse : jamais de `bold` (pas de faux gras, `font-synthesis: none`), interlettrage normal, pas de majuscules forcées.
   - *Fraunces italique* : un mot d'accent par titre, **sur les pages publiques seulement** (pas dans l'app).
   - *Archivo* : tout ce qu'on lit et tout ce qu'on clique — textes, boutons, menus — en casse normale.
   - *JetBrains Mono* : ce qui est machine : chemins, noms de fichiers, code, chiffres dans un tableau, raccourcis clavier.
   - Les majuscules espacées : un seul style, l'étiquette de section (`.label` : Archivo 600, 11 px, `letter-spacing .08em`, gris). Pas sur les boutons, pas sur les données.

3. **Des filets, pas des boîtes.** On sépare par des filets de 1 px (`ink-700`).
   Une boîte (bord + fond) seulement pour ce qui s'actionne : champ, bouton,
   fenêtre. Pas de carte posée sur une carte, pas de coins décoratifs, pas de
   pointillés (sauf la zone de dépôt d'un fichier).

4. **L'ombre franche est un geste, pas un effet.** Elle reste sur le bouton
   principal (et la barre flottante du landing). Les fenêtres et menus tiennent
   par un filet et un voile sombre derrière, pas par une ombre.

5. **Des chiffres honnêtes.** Pas de zéros ajoutés (« 5 », pas « 05 »),
   chiffres tabulaires (`tabular-nums`) dans les tableaux et compteurs.

6. **Le mouvement répond, il n'anime pas.** Retour d'appui, survol, apparition
   d'une section. Pas de pastille qui pulse, rien qui tourne en boucle (le
   bandeau défilant du landing est l'unique exception : c'est le geste punk du
   site). `prefers-reduced-motion` coupe tout.

7. **Pas de texture plaquée.** L'ancienne image de fond (servie par un autre
   domaine, contraire à notre « zéro requête externe ») est retirée : le
   caractère tient dans la typo, le vert, le bandeau et l'ombre du bouton.

8. **Le texte ne répète pas l'interface.** Pas d'étiquette qui dit ce que l'on
   voit déjà (« TÉLÉMÉTRIE », « I18N://URL »), pas de numérotation « 01 · »
   quand l'ordre n'a pas de sens, pas de puce « → » devant chaque ligne.

9. **Rythme.** L'app : dense, aligné, calme — 8 px de base. Le landing : de la
   place, et pas deux sections construites pareil.

## Signes d'« interface générée » à chasser

Étiquettes mono en majuscules partout · coins en crochets · pastilles qui
pulsent · cartes identiques en grille avec la même icône en haut à gauche ·
badges « Nouveau » · dégradés · ombres molles · emoji en guise d'icônes ·
chiffres à zéros · trois mots en capitales espacées au-dessus de chaque titre.

## Où ça vit

- Couleurs et thèmes : `web/static/tokens.css` (ne pas multiplier les teintes).
- App : `tools/appcss/` (composants `.btn-primary`, `.btn`, `.label`).
- Pages publiques : `web/static/brand.css`.
