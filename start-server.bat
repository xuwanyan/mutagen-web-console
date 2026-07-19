@echo off
chcp 65001 >nul
cd /d "%~dp0"
start "Mutagen Web Server" server\server.exe
