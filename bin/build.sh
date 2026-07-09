#!/usr/bin/bash

GOODPATH="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

cd $GOODPATH
cd ../

if [[ "$1" == "full" ]]; then
	echo "Updates..."
	go get -u ./...
	go mod tidy
fi

echo "Compilation..."
go build -ldflags "-s -w" -o nadaprod .

# Git
if [[ ! -z "$2" ]]; then
        echo "Commit GitHub..."
        gigit "$2"
fi

echo "Service"
systemctl stop nadaprod
systemctl start nadaprod
systemctl status nadaprod
