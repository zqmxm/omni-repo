@echo off
chcp 65001 >nul
cd /d "%~dp0"
echo 正在打包：自动操作助手.exe
go build -ldflags="-H=windowsgui -s -w" -o "dist\自动操作助手.exe" ./src
if errorlevel 1 (
  echo 打包失败，请确认已安装 Go 1.21 或更新版本
  exit /b 1
)
echo 完成：dist\自动操作助手.exe
