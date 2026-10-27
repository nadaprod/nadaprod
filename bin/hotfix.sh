#!/usr/bin/bash
# Correctif ciblé : publie UNIQUEMENT les fichiers nommés, depuis next/web vers
# web/ (production), sans embarquer le reste du chantier en cours.
#   bin/hotfix.sh static/tokens.css [autre/fichier …]   (chemins relatifs à web/)
# Sans argument : le correctif du moment (voir HOTFIX ci-dessous).
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")/.."

HOTFIX=(static/marker.css home.html debate.html solutions.html static/brand.css) # 2026-10-27 : lien Oliver Night → olivernight.nadaprod.com (+ traits de marqueur dans marker.css)
FILES=("${@:-${HOTFIX[@]}}")

for f in "${FILES[@]}"; do
	case "$f" in /* | *..*) echo "Chemin refusé : $f"; exit 1 ;; esac
	[[ -f "next/web/$f" ]] || { echo "Absent du staging : next/web/$f"; exit 1; }
	if [[ "$f" == *.css ]] && (( $(grep -o '/\*' "next/web/$f" | wc -l) != $(grep -o '\*/' "next/web/$f" | wc -l) )); then
		echo "Commentaires déséquilibrés dans $f — rien n'a été copié."; exit 1
	fi
done
for f in "${FILES[@]}"; do
	command cp -f "next/web/$f" "web/$f"
	chown www-data:www-data "web/$f"
	echo "ok  web/$f"
done
echo "/login → $(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:3004/login) (rechargez la page)"
