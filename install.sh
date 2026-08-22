#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SOURCE="$ROOT/dist/sub2api-linux-amd64"
TARGET_DIR="${HOME}/.local/bin"
TARGET="$TARGET_DIR/sub2api"

if [ ! -f "$SOURCE" ]; then
    printf '找不到 %s，请先运行 ./build.sh\n' "$SOURCE" >&2
    exit 1
fi

mkdir -p "$TARGET_DIR"
cp "$SOURCE" "$TARGET"
chmod +x "$TARGET"
printf '已安装：%s\n' "$TARGET"
printf '请确认 %s 已在 PATH 中，然后直接运行 sub2api。\n' "$TARGET_DIR"
