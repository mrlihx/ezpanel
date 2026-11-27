#!/usr/bin/env bash
set -Eeuo pipefail

here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
version=$(sed -n 's/^version: *//p' "$here/plugin.yaml" | head -1 | tr -d '"'\'' ')
out="$here/ezpanel-demo-plugin-$version.zip"

cd "$here/frontend"
if [[ ! -d node_modules ]]; then
  if [[ -f package-lock.json ]]; then
    npm ci
  else
    npm install
  fi
fi
npm run build

rm -rf "$here/package/bin"
mkdir -p "$here/package/bin/linux-amd64" "$here/package/bin/linux-arm64"
cd "$here/server"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" \
  -o "$here/package/bin/linux-amd64/server" .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" \
  -o "$here/package/bin/linux-arm64/server" .

cp "$here/plugin.yaml" "$here/README.md" "$here/package/"
find "$here/package" -name '.DS_Store' -delete

panel_pack="$here/../../scripts/pack-plugin.sh"
if [[ -x "$panel_pack" ]]; then
  "$panel_pack" -f -o "$out" "$here/package"
else
  stage=$(mktemp -d "$here/.pack-XXXXXXXX")
  trap 'rm -rf -- "$stage"' EXIT
  (cd "$here/package" && zip -qr "$stage/plugin.zip" plugin.yaml README.md bin web)
  mv -f -- "$stage/plugin.zip" "$out"
fi

echo "$out"
