#!/usr/bin/bash

GOODPATH="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cd $GOODPATH
cd ../

if [[ "$1" == "full" ]]; then
	echo "Updates..."
	go get -u ./...
	go mod tidy
fi

# Binaire standalone Tailwind v3 (compilation locale des sites publiés).
# Version figée : la v4 est CSS-first et ne lit plus les configs JS inline.
TW_VERSION="v3.4.17"
if [[ ! -x bin/tailwindcss ]]; then
	echo "Téléchargement de tailwindcss ${TW_VERSION} (standalone)..."
	curl -fsSL -o bin/tailwindcss \
		"https://github.com/tailwindlabs/tailwindcss/releases/download/${TW_VERSION}/tailwindcss-linux-x64" \
		&& chmod +x bin/tailwindcss \
		|| echo "Échec du téléchargement — la compilation locale restera désactivée (CDN conservé)."
fi

# CSS des pages de l'app (fini le CDN Tailwind dans notre propre UI) : purgé
# sur les 5 pages listées dans tools/appcss/tailwind.config.js.
if [[ -x bin/tailwindcss ]]; then
	echo "CSS de l'app (web/static/tw.css)..."
	bin/tailwindcss -c tools/appcss/tailwind.config.js -i tools/appcss/input.css \
		-o web/static/tw.css --minify \
		|| echo "Compilation du CSS de l'app échouée — tw.css existant conservé."
fi

echo "Compilation..."
go build -ldflags "-s -w" -o nadaprod ./cmd/nadaprod

# Git
if [[ ! -z "$2" ]]; then
        echo "Commit GitHub..."
        gigit "$2"
fi

echo "Service"
systemctl stop nadaprod
systemctl start nadaprod
systemctl status nadaprod
