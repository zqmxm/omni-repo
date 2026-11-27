@echo off
chcp 65001 >nul
cd /d "%~dp0"
echo ============================================
echo   Build FileAreaRenamer  -  single exe
echo ============================================
echo.

echo [1/2] Installing dependencies ...
REM 完整自带 OCR 的单文件 exe 请用已交付的 FileAreaRenamer.exe。
REM 这里自行打包时不会自动附带 OCR 模型；如需 OCR，请把 rec.onnx 放到 ocr\ 目录。
python -m pip install --disable-pip-version-check -q PySide6 pymupdf python-docx openpyxl xlrd==2.0.1 numpy onnxruntime pyinstaller pywin32
if errorlevel 1 goto ERR

echo [2/2] Packaging ...
python -m PyInstaller --onefile --windowed --clean --name FileAreaRenamer ^
  --collect-all PySide6 --collect-all pymupdf --collect-all openpyxl --collect-all xlrd ^
  --hidden-import win32com.client --hidden-import pythoncom ^
  --hidden-import fitz --hidden-import docx ^
  --exclude-module tkinter --exclude-module matplotlib --exclude-module numpy ^
  app.py
if errorlevel 1 goto ERR

echo.
echo ============================================
echo   DONE  -^>  dist\FileAreaRenamer.exe
echo   (only ONE exe file, no extra files needed)
echo.
echo   External engine (NOT bundled):
echo     - Install LibreOffice  OR  MS Office
echo     - or put portable LibreOffice here:
echo       libreoffice\program\soffice.exe
echo ============================================
pause
exit /b 0

:ERR
echo.
echo BUILD FAILED. Check python / pip / network, then retry.
pause
exit /b 1
