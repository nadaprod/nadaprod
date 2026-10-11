#!/usr/bin/bash
# Met en ligne la refonte /sites + /drafts (personnalités + recherche globale)
# préparée et testée dans le scratchpad de la session Claude. Pas de rebuild
# ni de redémarrage : ./web est servi tel quel par le service.
set -euo pipefail

STAGE="/tmp/claude-0/-intelligences-nadaprod/16150f66-0f81-4efe-8f80-03868ef618f7/scratchpad/stage"
cd "$(dirname "$(readlink -f "$0")")/.."

# Dépendances d'abord (script, traductions, CSS), les pages ensuite : une page
# neuve ne se charge jamais sans ce qu'elle référence.
FILES=(
	web/static/sites-app.js
	web/static/i18n/fr.json
	web/static/i18n/en.json
	web/static/tw.css
	tools/appcss/tailwind.config.js
	web/sites.html
	web/drafts.html
)

for f in "${FILES[@]}"; do
	[[ -f "$STAGE/$f" ]] || { echo "Manquant : $STAGE/$f — rien n'a été copié."; exit 1; }
done
for f in "${FILES[@]}"; do
	command cp -f "$STAGE/$f" "$f"
	chown www-data:www-data "$f"
	echo "ok  $f"
done

# Serveur de test de la session (port 18121), s'il tourne encore.
pid=$(ss -ltnp 2>/dev/null | grep ':18121 ' | grep -o 'pid=[0-9]*' | cut -d= -f2 || true)
[[ -n "$pid" ]] && kill "$pid" && echo "Serveur de test 18121 arrêté."

for p in /sites /drafts /static/sites-app.js; do
	echo "$p → $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:3004$p")"
done
echo "En ligne. Ancienne version : scratchpad/pre-refactor.tgz"
