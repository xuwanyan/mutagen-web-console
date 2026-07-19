@echo off
chcp 65001 >nul
title Install Agent Auto-Start
setlocal enabledelayedexpansion

echo ============================================
echo  Mutagen Web Agent - Auto-Start Installer
echo ============================================
echo.

:: --- Configuration (modify these) ---
set SERVER_URL=ws://192.168.238.23:18080/ws/agent
set AGENT_NAME=rpa-pc
set MUTAGEN_DIR=C:\mutagen
set AGENT_EXE=%MUTAGEN_DIR%\agent.exe
:: -----------------------------------

:: Check if running as admin
net session >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo [!] Please run as Administrator!
    echo     Right-click ^> Run as administrator
    pause
    exit /b 1
)

:: Check files exist
if not exist "%AGENT_EXE%" (
    echo [!] agent.exe not found at: %AGENT_EXE%
    pause
    exit /b 1
)
if not exist "%MUTAGEN_DIR%\mutagen.exe" (
    echo [!] mutagen.exe not found at: %MUTAGEN_DIR%\mutagen.exe
    pause
    exit /b 1
)

echo.
echo ============================================
echo  Method 1: Task Scheduler (recommended, built-in)
echo ============================================

echo [1/3] Removing old task if exists...
schtasks /delete /tn "MutagenAgent" /f 2>nul

echo [2/3] Creating scheduled task (runs at system startup)...
schtasks /create /tn "MutagenAgent" /tr "%AGENT_EXE% -server %SERVER_URL% -name %AGENT_NAME%" /sc onstart /ru SYSTEM /rl highest /f

echo [3/3] Starting task now...
schtasks /run /tn "MutagenAgent"

echo.
echo ============================================
echo  Method 2: nssm (proper Windows Service)
echo  Install nssm from: https://nssm.cc/download
echo ============================================
echo.
echo  If you prefer nssm, run:
echo    nssm install MutagenAgent "%AGENT_EXE%" "-server %SERVER_URL% -name %AGENT_NAME%"
echo    nssm start MutagenAgent
echo.

:: Verify
echo.
echo Checking task status...
schtasks /query /tn "MutagenAgent" /fo LIST /v 2>nul | find "Status:"
if %ERRORLEVEL% equ 0 (
    echo.
    echo [OK] Auto-start task installed!
    echo       Task Name: MutagenAgent
    echo       Agent:     %AGENT_NAME%
    echo       Server:    %SERVER_URL%
    echo       Will run:  At system startup (before logon)
) else (
    echo [!] Task may not have been created.
)

echo.
echo ============================================
echo  To manage:
echo    Start:   schtasks /run /tn MutagenAgent
echo    Stop:    taskkill /F /IM agent.exe
echo    Status:  schtasks /query /tn MutagenAgent
echo    Remove:  schtasks /delete /tn MutagenAgent /f
echo ============================================
pause
endlocal