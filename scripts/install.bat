@echo off
title Mutagen Web Agent - Install

set MUTAGEN_DIR=C:\mutagen
set SCRIPT_DIR=%~dp0

echo ========================================
echo Mutagen Web Agent - Installing
echo ========================================
echo.

reg query "HKU\S-1-5-19" >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Please run as Administrator!
    pause
    exit /b 1
)

echo [1/4] Copying files to %MUTAGEN_DIR%...
if not exist "%MUTAGEN_DIR%" mkdir "%MUTAGEN_DIR%"
copy /Y "%SCRIPT_DIR%mutagen.exe" "%MUTAGEN_DIR%\"
copy /Y "%SCRIPT_DIR%mutagen-agents.tar.gz" "%MUTAGEN_DIR%\"
copy /Y "%SCRIPT_DIR%mutagen-web-agent.exe" "%MUTAGEN_DIR%\"
copy /Y "%SCRIPT_DIR%agent-config.json" "%MUTAGEN_DIR%\"
echo OK

echo [2/4] Setting environment variables...
setx MUTAGEN_PATH "C:\mutagen\mutagen.exe"
powershell -NoProfile -Command "$p = [Environment]::GetEnvironmentVariable('PATH','User'); if ($p -and $p -notlike '*C:\mutagen*') { [Environment]::SetEnvironmentVariable('PATH', $p + ';C:\mutagen', 'User') } elseif (-not $p) { [Environment]::SetEnvironmentVariable('PATH', 'C:\mutagen', 'User') }"
echo OK

echo [3/4] Registering auto-start task...
schtasks /create /tn "MutagenWebAgent" /tr "%MUTAGEN_DIR%\mutagen-web-agent.exe --config %MUTAGEN_DIR%\agent-config.json -log %MUTAGEN_DIR%\agent.log" /sc onlogon /ru %USERNAME% /rl highest /f
echo OK

echo [4/4] Windows Service
echo.
echo To register service manually, run:
echo.
echo   sc create MutagenWebAgent binPath= "%MUTAGEN_DIR%\mutagen-web-agent.exe --config %MUTAGEN_DIR%\agent-config.json -log %MUTAGEN_DIR%\agent.log" start= auto obj= ".\%USERNAME%" password= "YOUR_PASSWORD"
echo.
echo Then start:
echo   sc start MutagenWebAgent
echo.
echo ========================================
echo  Install completed!
echo ========================================
pause
