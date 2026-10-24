# /debate — mesurer l'impact de l'IA et du statique

« Un débat, des faits » : une page publique qui met face à face ce que l'IA
consomme dans NADAPROD et ce que le statique économise à la publication, avec
nos chiffres réels et des hypothèses sourcées, affichées et réglables.

## Principes

- **Mesuré chez nous, pas estimé à la louche** : chaque appel Gemini et chaque
  requête servie à un site publié est compté par le serveur lui-même.
- **Aucun traceur** : agrégats par jour (et par site / type), jamais d'IP, de
  user-agent, de cookie ni d'URL conservés. Un robot est reconnu à la volée
  (user-agent) et compté à part, rien n'est stocké de plus.
- **Hypothèses visibles** : ce qui n'est pas mesurable chez nous (énergie d'une
  requête Gemini, coût d'une page WordPress) est une hypothèse sourcée, avec
  sa valeur par défaut, réglable par le lecteur.
- GoAccess / journaux Caddy : inutiles pour la mesure courante (root seulement,
  ~3 semaines d'historique). Un import ponctuel d'historique reste possible.

## Données (`pkg/stats`, SQLite `STATS_FILE`, défaut `./stats.db`)

```sql
-- Requêtes servies aux sites publiés (HostMiddleware), cumul par jour et site.
hits_daily(day TEXT, site TEXT, requests INT, pages INT, bots INT, bytes INT, dur_us INT,
           PRIMARY KEY(day, site))
-- pages = réponses HTML 2xx (ce qu'un CMS aurait recalculé) ; dur_us = somme des
-- temps de service des pages (µs), pour la médiane approchée : dur_us / pages.

-- Appels IA (client Gemini), cumul par jour, nature et modèle.
ai_daily(day TEXT, kind TEXT, model TEXT, requests INT, failures INT,
         prompt_tokens INT, output_tokens INT, ms INT,
         PRIMARY KEY(day, kind, model))
-- kind : type de bloc (hero, faq…, "element" = retouche d'élément, "section"),
-- ou "translate".
```

Les compteurs de visites sont tenus en mémoire et écrits toutes les 60 s, à
chaque lecture du résumé et à l'arrêt (SIGINT/SIGTERM) ; les appels IA, peu
nombreux, sont écrits à chaque appel. Une requête de robot compte dans
`requests`, `bytes` et `bots`, jamais dans `pages` ni dans le temps de service.

## Contrats Go

```go
// pkg/stats
func Open(path string) (*Store, error)
func (s *Store) Close() error                        // vide le tampon des visites
func (s *Store) Hit(site string, status int, bytes int64, dur time.Duration, html, bot bool)
func (s *Store) AI(e AIEvent)
type AIEvent struct { Kind, Model string; OK bool; PromptTokens, OutputTokens int; Duration time.Duration }
func IsBot(userAgent string) bool
func (s *Store) Summary(ctx, days int) (Summary, error) // agrégats publics (voir l'API)
func (s *Store) SiteSummary(ctx) ([]SiteStats, error)  // détail par site (root)
func Register(public, root *gin.RouterGroup, d Deps)   // Deps{Store, Counts func(ctx) (AppCounts, error), TTL}

// pkg/projects : Store.Counts(ctx) (Counts{Projects, Blocks, Versions, Translations}, error)
// pkg/sites    : Store.Counts() (sites, live, pages int, err error)

// pkg/gemini : Options.OnUsage func(UsageEvent) — appelé après chaque appel
// (génération, flux, traduction), succès ou échec ; tokens lus dans
// usageMetadata (promptTokenCount, candidatesTokenCount ; en flux : dernier fragment).
type UsageEvent struct { Kind, Model string; OK bool; PromptTokens, OutputTokens int; Duration time.Duration }

// pkg/sites : Store.OnHit func(site string, status int, bytes int64, dur time.Duration, html bool, userAgent string)
// appelé par HostMiddleware après chaque requête servie sur un domaine branché.
```

## API

`GET /api/stats` — **public**, mis en cache 60 s :

```json
{
  "since": "2026-10-25",                 // premier jour mesuré
  "updated_at": "…",
  "ai": {
    "requests": 0, "failures": 0, "prompt_tokens": 0, "output_tokens": 0, "ms": 0,
    "by_kind": [{"kind": "hero", "requests": 0, "output_tokens": 0}],
    "daily": [{"day": "2026-10-25", "requests": 0, "output_tokens": 0}]
  },
  "static": {
    "requests": 0, "pages": 0, "bots": 0, "bytes": 0, "page_us": 0,   // page_us : temps moyen de service d'une page
    "daily": [{"day": "…", "requests": 0, "pages": 0}]
  },
  "app": {                                 // état actuel, depuis les stores existants
    "projects": 0, "blocks": 0,
    "versions": {"generate": 0, "variant": 0, "retouch": 0, "retheme": 0, "edit": 0, "code": 0, "import": 0, "image": 0},
    "translations": {"machine": 0, "human": 0},
    "sites": 0, "live_sites": 0, "pages": 0
  }
}
```

`daily` couvre les 30 derniers jours (jours sans activité inclus, à 0). Aucune
donnée par site dans la réponse publique ; `GET /api/stats/sites` (root) donne
le détail par site des visites.

## Hypothèses par défaut (page /debate, réglables)

| Hypothèse | Défaut | Source / raison |
|---|---|---|
| Énergie d'une requête IA | 0,24 Wh | Google, « Measuring the environmental impact of delivering AI at Google scale » (août 2025) : requête texte médiane Gemini. Nos générations sont plus longues que la médiane : c'est un plancher, réglable ×1 à ×20. |
| Temps CPU d'une page recalculée par un CMS (WordPress sans cache) | 150 ms | ordre de grandeur courant d'un rendu PHP + requêtes SQL ; réglable 30–1000 ms |
| Temps de service d'une page statique | mesuré (`page_us`) | nos propres compteurs |
| Puissance d'un cœur de serveur occupé | 10 W | ordre de grandeur ; réglable 3–30 W |
| Intensité carbone de l'électricité | 50 g CO₂e/kWh | ordre de grandeur du mix français ; réglable |

Bilan : `énergie IA = requêtes IA × Wh/requête` ;
`économie par page = (t_cms − t_statique) × P_cœur / 3600` ;
`pages nécessaires pour compenser une génération = Wh/requête ÷ économie par page`.
La page affiche les formules, pas seulement le résultat.
