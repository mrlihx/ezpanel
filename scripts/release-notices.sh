#!/bin/bash
set -euo pipefail

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
notices="$work/third-party-notices"
mkdir -p "$notices"

copy_notices() {
  local source=$1 target=$2
  local file
  mkdir -p "$target"
  for file in "$source"/*; do
    [[ -f $file ]] || continue
    case $(basename "$file" | tr '[:lower:]' '[:upper:]') in
      LICENSE*|LICENCE*|COPYING*|NOTICE*|COPYRIGHT*) cp "$file" "$target/" ;;
    esac
  done
}

for arch in amd64 arm64; do
  GOOS=linux GOARCH=$arch CGO_ENABLED=0 go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}{{"\t"}}{{.Version}}{{"\t"}}{{.Dir}}{{end}}{{end}}' ./cmd/ezpanel
done | awk 'NF' | sort -u > "$work/go-modules"
cut -f 1,2 "$work/go-modules" > "$notices/go-modules.tsv"
while IFS=$'\t' read -r module version dir; do
  copy_notices "$dir" "$notices/go/$module@$version"
done < "$work/go-modules"

web_dir=$(cd web && pwd)
npm --prefix "$web_dir" ls --all --omit=dev --parseable > "$work/npm-packages"
while IFS= read -r dir; do
  [[ $dir != "$web_dir" ]] || continue
  copy_notices "$dir" "$notices/npm/${dir#"$web_dir/node_modules/"}"
done < "$work/npm-packages"
cp "$(go env GOROOT)/LICENSE" "$notices/Go-LICENSE"
cp web/package-lock.json "$notices/npm-package-lock.json"
tar -czf dist/third-party-notices.tar.gz -C "$work" third-party-notices
