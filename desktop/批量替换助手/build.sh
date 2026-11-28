
#!/usr/bin/env bash
# 构建批量替换助手：默认产出当前平台，CROSS=1 时交叉编译三平台
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p dist
export CGO_ENABLED=0

build_windows() {
  GOOS=windows GOARCH=amd64 go build -ldflags="-H=windowsgui -s -w" -o "dist/批量替换助手.exe" ./src
  GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o "dist/批量替换助手-CLI.exe" ./src
  echo "  Windows: dist/批量替换助手.exe (GUI) / dist/批量替换助手-CLI.exe (CLI)"
}
build_linux() {
  GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "dist/batchreplace-linux-amd64" ./src
  echo "  Linux:   dist/batchreplace-linux-amd64"
}
build_macos() {
  GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o "dist/batchreplace-macos-arm64" ./src
  echo "  macOS:   dist/batchreplace-macos-arm64"
}

if [[ "${CROSS:-0}" == "1" ]]; then
  echo "交叉编译全部平台..."
  build_windows
  build_linux
  build_macos
else
  echo "构建当前平台..."
  case "$(go env GOOS)" in
    windows) build_windows ;;
    linux)   build_linux ;;
    darwin)  build_macos ;;
    *)       go build -ldflags="-s -w" -o "dist/batchreplace" ./src; echo "  dist/batchreplace" ;;
  esac
fi
echo "完成。CLI 用法：dist/batchreplace --help（或 批量替换助手-CLI.exe --help）"
