#!/usr/bin/bash
# Ouvre au public la seule route GET /api/stats sur nadaprod.com (la page /debate
# en a besoin). Le reste du verrou « @rootes » (/sites, /drafts, /editor, /api…)
# reste réservé aux IP autorisées. /api/stats ne contient que des totaux par jour.
#   - sauvegarde /etc/Caddyfile, ajoute « not path /api/stats » au matcher,
#   - valide la config (restaure la sauvegarde si invalide), recharge Caddy,
#   - vérifie que https://nadaprod.com/api/stats répond 200.
# Idempotent : ne fait rien si l'exception est déjà là.
set -euo pipefail
CONF=/etc/Caddyfile
CADDY=/home/caddy/bin/caddy

if awk '/^nadaprod.com \{/,/^\}/' "$CONF" | grep -q 'not path /api/stats'; then
	echo "Déjà ouvert."; exit 0
fi

BAK="$CONF.bak-$(date +%Y%m%d-%H%M%S)"
cp -p "$CONF" "$BAK"
echo "Sauvegarde : $BAK"

# Dans le bloc nadaprod.com, juste après la ligne « path /sites* … /api* » du matcher @rootes.
awk '
	/^nadaprod.com \{/ { inblock = 1 }
	inblock && /^\}/ { inblock = 0 }
	{ print }
	inblock && !done && /path \/sites\* \/drafts\* \/editor\* \/api\*/ {
		match($0, /^[ \t]*/); print substr($0, 1, RLENGTH) "not path /api/stats"; done = 1
	}
' "$BAK" > "$CONF"

if ! awk '/^nadaprod.com \{/,/^\}/' "$CONF" | grep -q 'not path /api/stats'; then
	cp -p "$BAK" "$CONF"; echo "Matcher introuvable — rien n'a changé."; exit 1
fi
if ! "$CADDY" validate --config "$CONF" --adapter caddyfile >/dev/null 2>&1; then
	cp -p "$BAK" "$CONF"; echo "Config invalide — sauvegarde restaurée, rien n'a changé."; exit 1
fi
"$CADDY" reload --config "$CONF" --adapter caddyfile || systemctl reload caddy
sleep 1
echo "https://nadaprod.com/api/stats → $(curl -s -o /dev/null -w '%{http_code}' https://nadaprod.com/api/stats)"
echo "https://nadaprod.com/api/sites → $(curl -s -o /dev/null -w '%{http_code}' https://nadaprod.com/api/sites) (doit rester 403)"
