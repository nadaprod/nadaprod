# NADAPROD v7 alpha

Éditeur visuel de pages Tailwind par blocs, propulsé par Gemini Flash (latest) et un **générateur ultra-spécialisé par type de bloc** : chaque bloc (en-tête, hero, fonctionnalités, tarifs, témoignages, FAQ, CTA, footer) possède son propre prompt système expert, avec un contrat de sortie strict (fragment HTML Tailwind unique, responsive, accessible, sans lorem ipsum).

## Architecture

```
windpage/
├── main.go       # Serveur gin-gonic : static + API
├── gemini.go     # Client REST Gemini (generateContent) + nettoyage du HTML
├── prompts.go    # Registre des générateurs spécialisés (1 prompt expert / bloc)
├── go.mod
└── web/
    └── index.html  # Studio : palette de blocs, aperçu live, panneau IA + code
```

### API

| Route | Description |
|---|---|
| `GET /api/health` | État + modèle actif |
| `GET /api/blocks` | Catalogue des blocs (labels, presets) — les prompts système restent côté serveur |
| `POST /api/generate` | `{ block, prompt, brand{name,colors,tone}, current_html }` → `{ html, model, elapsed_ms }` |

`current_html` est renvoyé à Gemini comme contexte : un second prompt sur le même bloc **itère** au lieu de repartir de zéro.

## Lancement

La configuration se fait via un fichier `.env` (chargé au démarrage) ou des variables d'environnement — ces dernières ont la priorité.

```bash
cp .env.example .env      # puis renseignez GEMINI_API_KEY
go mod tidy
go run .
# → http://localhost:8080
```

| Variable | Défaut | Rôle |
|---|---|---|
| `GEMINI_API_KEY` | — (obligatoire) | Clé Gemini — https://aistudio.google.com/apikey |
| `GEMINI_MODEL` | `gemini-flash-latest` | Modèle (alias du dernier Flash stable) |
| `ADDR` | `:8080` | Adresse d'écoute HTTP (`host:port`) |
| `SITES_DIR` | `./sites` | Racine de stockage des sites importés |
| `GIN_MODE` | *(release)* | `debug` pour les logs détaillés |

## Utilisation

1. Onglet **Marque** : nom, palette, ton — injectés dans chaque génération.
2. Rail gauche : ajoutez des blocs (En-tête, Hero, Tarifs…), réordonnez ↑↓.
3. Panneau **Générer** : choisissez un preset ou écrivez votre brief, `⌘/Ctrl + Entrée` pour générer.
4. Re-promptez le même bloc pour le retoucher (« passe le fond en clair ») — l'IA part de la version actuelle.
5. Onglet **Code** : éditez le HTML Tailwind à la main, « Appliquer ».
6. Barre haute : bascule Bureau / Tablette / Mobile, **Copier la page**, **Exporter le HTML** (fichier autonome avec Tailwind CDN).

Le projet (blocs + marque) est persisté en `localStorage`.

## Étendre les générateurs

Ajoutez une entrée dans `blockSpecs` (`prompts.go`) : type, label, presets et prompt système spécialisé. Le bloc apparaît automatiquement dans la palette de l'éditeur.

## Sites importés (v0.2)

Trois primitives, zéro base de données :

1. **Import** — `POST /api/sites` avec un `.zip` ou `.tar.gz` (magic bytes sniffés, dossier racine unique aplati automatiquement). Un site = un dossier `sites/<id>/public/` servi tel quel + un `site.json` d'une ligne.
2. **Édition universelle** — `/edit/<id>/<page>` sert la vraie page avec un script injecté avant `</body>` : clic = sélection, double-clic = édition du texte, panneau IA pour retoucher l'élément sélectionné (générateur `element`, qui préserve la stack de la page). « Enregistrer » sérialise le DOM (artefacts d'édition retirés) et réécrit le fichier.
3. **Domaine** — `PUT /api/sites/<id>/domain`. Un middleware gin en tête de chaîne compare `Host` à la table domaine→site : si ça matche, le site est servi directement. Pointez un enregistrement A vers le serveur, c'est en ligne.

Interface : `http://localhost:8080/sites`. Pour le HTTPS multi-domaines en production, placez Caddy devant (`on_demand_tls`) ou ajoutez `autocert`.
