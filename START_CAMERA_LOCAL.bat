@echo off
cd /d "%~dp0"
if exist "%~dp0WebcamLowLatency.exe" (
    "%~dp0WebcamLowLatency.exe" local
    exit /b
)
set "PY=python"
python --version >nul 2>&1
if errorlevel 1 set "PY=py -3"
%PY% --version >nul 2>&1
if errorlevel 1 (echo Install Python 3 or download the portable ZIP from GitHub Releases.& pause & exit /b 1)
%PY% -c "import cv2, PIL, numpy" >nul 2>&1
if errorlevel 1 %PY% -m pip install -r requirements.txt
if errorlevel 1 (pause & exit /b 1)
%PY% camera.py local
if errorlevel 1 pause
