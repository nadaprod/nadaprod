#!/bin/sh
set -e

echo "🔄 Updating project"
git pull
npm install
npm run build
