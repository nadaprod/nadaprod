#!/usr/bin/bash
# Staging → production. Ce dépôt EST la production (./web servi en direct) :
# le front se prépare dans next/web (copie gitignorée de web/), se teste avec
# tools/e2e/run.sh (qui sert next/web s'il existe), puis ce script le met en ligne.
#
#   bin/next.sh --fresh    (re)crée next/ depuis la production : nouveau chantier
#   bin/next.sh            met en ligne next/web (front seul, sans redémarrage)
#   bin/next.sh --build    idem + vérifs Go (vet, tests) puis bin/build.sh
#                          (CSS de l'app, binaire, redémarrage) — si le Go a changé
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")/.."

if [[ "${1:-}" == "--fresh" ]]; then
	mkdir -p next/web next/tools
	rsync -a --delete --exclude webfonts --exclude webfonts.tgz web/ next/web/
	rsync -a --delete tools/appcss next/tools/
	ln -sfn "$PWD/web/webfonts" next/web/webfonts # miroir de polices (1,8 Go) : partagé, jamais copié
	echo "next/ = production. Éditez next/web, testez : tools/e2e/run.sh"
	exit 0
fi

[[ -d next/web ]] || { echo "next/web absent — bin/next.sh --fresh pour commencer un chantier."; exit 1; }

if [[ "${1:-}" == "--build" ]]; then
	echo "Vérifications Go…"
	go vet ./cmd/... ./pkg/...
	go test ./pkg/... >/tmp/nadaprod-next-tests.log 2>&1 || { tail -30 /tmp/nadaprod-next-tests.log; echo "Tests en échec — rien n'a été copié."; exit 1; }
	go build -o /dev/null ./cmd/nadaprod
fi

# --delay-updates : les fichiers apparaissent d'un coup (une page ne se charge
# jamais sans le script qu'elle référence). Pas de --delete : un fichier retiré
# du staging reste en production (à supprimer à la main, volontairement).
echo "Front :"
rsync -rc --delay-updates --exclude webfonts --exclude webfonts.tgz --itemize-changes next/web/ web/ | grep '^>' || echo "  (rien à copier)"
rsync -rc next/tools/appcss/ tools/appcss/ # config Tailwind + composants (input.css)
chown -R www-data:www-data web tools/appcss

if [[ "${1:-}" == "--build" ]]; then
	bin/build.sh
	sleep 2
fi
for p in /api/health /sites /editor; do
	echo "$p → $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:3004$p")"
done
