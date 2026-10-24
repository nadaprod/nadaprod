#!/usr/bin/bash
# Tests navigateur (chromium headless) contre un serveur NADAPROD isolé :
# binaire compilé à part, données jetables, clé Gemini factice, port libre.
# Front testé : next/web s'il existe (staging), sinon web/.
#
#   tools/e2e/run.sh                 # tous les scénarios
#   tools/e2e/run.sh explorer        # un seul (home | debate | sites | explorer | studio-i18n)
# Captures : $E2E_OUT (défaut /tmp/nadaprod-e2e).
set -euo pipefail
ROOT="$(cd "$(dirname "$(readlink -f "$0")")/../.." && pwd)"
E2E="$ROOT/tools/e2e"
OUT="${E2E_OUT:-/tmp/nadaprod-e2e}"
WEB="$ROOT/web"; [[ -d "$ROOT/next/web" ]] && WEB="$ROOT/next/web"
SCENARIOS=("${@:-home debate sites explorer studio-i18n}"); SCENARIOS=(${SCENARIOS[*]})

TMP="$(mktemp -d)"
PID=""
cleanup() { [[ -n "$PID" ]] && kill "$PID" 2>/dev/null || true; rm -rf "$TMP"; }
trap cleanup EXIT

echo "Front : $WEB"
(cd "$ROOT" && go build -o "$TMP/np" ./cmd/nadaprod)
mkdir -p "$TMP/run/data" "$OUT"
ln -s "$WEB" "$TMP/run/web"

PORT=18200
while ss -ltn | grep -q ":$PORT "; do PORT=$((PORT + 1)); done
B="http://127.0.0.1:$PORT"
(cd "$TMP/run" && ADDR="127.0.0.1:$PORT" GEMINI_API_KEY=dummy ROOT_PASSWORD=smokepass123 \
	SITES_DIR="$TMP/run/data/sites" USERS_FILE="$TMP/run/data/users.json" WAITLIST_FILE="$TMP/run/data/wl.json" \
	DB_FILE="$TMP/run/data/db.sqlite" TAILWIND_BIN="$ROOT/bin/tailwindcss" exec "$TMP/np" >"$TMP/server.log" 2>&1) &
PID=$!
for _ in $(seq 50); do curl -sf "$B/api/health" >/dev/null && break; sleep 0.2; done

# ---------- Données ----------
J="$TMP/jar"
api() { curl -sf -b "$J" -c "$J" -H 'Content-Type: application/json' "$@" >/dev/null; }
api -d '{"email":"web@l3dlp.com","password":"smokepass123"}' "$B/api/login"

site() { # site <id> [fichiers supplémentaires…] : index + contact, archivé et importé
	local name="$1" d="$TMP/src/$1"; mkdir -p "$d"
	printf '<!doctype html><html><head><script src="https://cdn.tailwindcss.com"></script></head><body class="p-10"><h1 class="text-4xl font-bold">%s</h1></body></html>' "$1" >"$d/index.html"
	printf '<!doctype html><html><body><p>contact</p></body></html>' >"$d/contact.html"
	shift
	for f in "$@"; do mkdir -p "$(dirname "$d/$f")"; printf '/* %s */\n' "$f" >"$d/$f"; done
	(cd "$TMP/src" && zip -qr "$name.zip" "$name")
	curl -sf -b "$J" -F "archive=@$TMP/src/$name.zip" "$B/api/sites" >/dev/null
}
seed_sites() {
	local n
	for n in boulangerie-lyon portfolio-ines cafe-des-arts landing-ete zebra-studio; do site "$n"; done
	api -X PUT -d '{"domain":"boulangerie-lyon.fr"}' "$B/api/sites/boulangerie-lyon/domain"
	api -X PUT -d '{"domain":"ines.design"}' "$B/api/sites/portfolio-ines/domain"
	api -d '{"email":"alice@test.fr","password":"alicepass1"}' "$B/api/users"
	api -X PUT -d '{"sites":["cafe-des-arts","portfolio-ines"]}' "$B/api/users/alice@test.fr"
}
seed_demo() { # site à dossiers pour l'explorateur
	local d="$TMP/src/demo"; mkdir -p "$d/fr" "$d/css" "$d/js"
	printf '<!doctype html><html><head><link rel="stylesheet" href="css/site.css"></head><body><h1 class="title">Accueil</h1><script src="js/app.js"></script></body></html>' >"$d/index.html"
	printf '<!doctype html><html><body><p>Contact</p></body></html>' >"$d/contact.html"
	printf '<!doctype html><html><body><p>Bonjour</p></body></html>' >"$d/fr/index.html"
	printf '.title{color:#7c3aed}\n' >"$d/css/site.css"; printf 'body{margin:0}\n' >"$d/css/extra.css"; printf 'console.log(1)\n' >"$d/js/app.js"
	(cd "$TMP/src" && zip -qr demo.zip demo)
	curl -sf -b "$J" -F "archive=@$TMP/src/demo.zip" "$B/api/sites" >/dev/null
}

# sites attend exactement 5 sites ; l'explorateur ajoute le sien après.
fails=0
for s in "${SCENARIOS[@]}"; do
	case "$s" in
	sites) seed_sites ;;
	explorer) seed_demo ;;
	esac
	echo "── $s"
	node "$E2E/$s.mjs" "$B" "$OUT" || fails=$((fails + 1))
done
echo "Captures : $OUT"
if ((fails)); then echo "$fails scénario(s) en échec — journal serveur :"; tail -20 "$TMP/server.log"; exit 1; fi
