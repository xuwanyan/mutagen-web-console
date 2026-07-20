@echo off
title Mutagen Web Agent - Install

set MUTAGEN_DIR=C:\mutagen
set SCRIPT_DIR=%%~dp0

echo ========================================
echo Mutagen Web Agent - Installing
echo ========================================
echo.

net session >nul 2>&1
if %%ERRORLEVEL%% neq 0 (
    echo [ERROR] Please run as Administrator!
    pause
    exit /b 1
)

echo [1/3] Copying files to %%MUTAGEN_DIR%%...
if not exist "%%MUTAGEN_DIR%%" mkdir "%%MUTAGEN_DIR%%"
copy /Y "%%SCRIPT_DIR%%mutagen.exe" "%%MUTAGEN_DIR%%\"
copy /Y "%%SCRIPT_DIR%%mutagen-agents.tar.gz" "%%MUTAGEN_DIR%%\"
copy /Y "%%SCRIPT_DIR%%mutagen-web-agent.exe" "%%MUTAGEN_DIR%%\"
copy /Y "%%SCRIPT_DIR%%agent-config.json" "%%MUTAGEN_DIR%%\"
echo OK

echo [2/3] Registering auto-start task...
schtasks /create /tn "MutagenWebAgent" /tr "%%MUTAGEN_DIR%%\mutagen-web-agent.exe --config %%MUTAGEN_DIR%%\agent-config.json -log %%MUTAGEN_DIR%%\agent.log" /sc onlogon /ru %%USERNAME%% /rl highest /f
echo OK

echo [3/3] Windows Service
echo.
echo To register service manually, run:
echo.
echo   sc create MutagenWebAgent binPath= "%%MUTAGEN_DIR%%\mutagen-web-agent.exe --config %%MUTAGEN_DIR%%\agent-config.json -log %%MUTAGEN_DIR%%\agent.log" start= auto obj= ".\%%USERNAME%%" password= "YOUR_PASSWORD"
echo.
echo Then start:
echo   sc start MutagenWebAgent
echo.
echo ========================================
echo  Install completed!
echo ========================================
pause
