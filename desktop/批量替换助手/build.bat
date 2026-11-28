
@echo off
chcp 65001 >nul
cd /d "%~dp0"
set CGO_ENABLED=0
if not exist dist mkdir dist

echo [1/2] 打包桌面版（单 exe，双击直接弹窗口）...
go build -ldflags="-H=windowsgui -s -w" -o "dist\批量替换助手.exe" ./src
if errorlevel 1 goto fail

echo [2/2] 打包命令行版（跨平台 CLI 的 Windows 构建）...
go build -ldflags="-s -w" -o "dist\批量替换助手-CLI.exe" ./src
if errorlevel 1 goto fail

echo.
echo 完成：
echo   桌面版  dist\批量替换助手.exe        （双击直接弹原生窗口，不占端口）
echo   命令行  dist\批量替换助手-CLI.exe --help
echo.
echo 交叉编译 Linux / macOS 版本：CROSS=1 bash build.sh
exit /b 0

:fail
echo 打包失败，请确认已安装 Go 1.21 或更新版本
exit /b 1
