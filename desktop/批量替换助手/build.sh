#!/usr/bin/env bash
# 在 Linux / Mac 上交叉编译 Windows 64 位 exe
set -e
cd "$(dirname "$0")"
mkdir -p dist
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -ldflags="-H=windowsgui -s -w" -o "dist/批量替换助手.exe" ./src
echo "完成：dist/批量替换助手.exe"
