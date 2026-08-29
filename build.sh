#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
DIST="$ROOT/dist"
VERSION="0.1.0"

mkdir -p "$DIST"
cd "$ROOT"
go test ./...
LDFLAGS="-s -w -X main.version=$VERSION"

GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "$DIST/sub2api-windows-amd64.exe" .
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "$DIST/sub2api-linux-amd64" .
cp "$DIST/sub2api-windows-amd64.exe" "$DIST/sub2api.exe"

printf 'Built: %s\n' "$DIST/sub2api.exe"
printf 'Built: %s\n' "$DIST/sub2api-windows-amd64.exe"
printf 'Built: %s\n' "$DIST/sub2api-linux-amd64"
