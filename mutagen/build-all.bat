@echo off
chcp 65001 >nul
cd /d "%~dp0"

echo [1/4] Building mutagen...
go run scripts\build.go
if %errorlevel% neq 0 (
    echo mutagen build failed
    exit /b 1
)

echo [2/4] Copying mutagen binaries to web-console...
copy /Y build\mutagen.exe web-console\agent\mutagen.exe
if %errorlevel% neq 0 exit /b 1
copy /Y build\mutagen-agents.tar.gz web-console\agent\mutagen-agents.tar.gz
if %errorlevel% neq 0 exit /b 1

echo [3/4] Building web-console server and agent...
cd web-console\server
go build -o server.exe .
if %errorlevel% neq 0 exit /b 1
cd ..\agent
go build -o agent.exe .
if %errorlevel% neq 0 exit /b 1
cd ..

echo [4/4] Building frontend...
cd web
call npm install
if %errorlevel% neq 0 exit /b 1
call npm run build
if %errorlevel% neq 0 exit /b 1
cd ..

echo Done.
echo.
echo Deploy files:
echo   - web-console\server\server.exe  (cloud server)
echo   - web-console\web\dist           (cloud server)
echo   - web-console\agent\agent.exe    (each Windows machine)
echo   - web-console\agent\mutagen.exe  (each Windows machine)
echo   - web-console\agent\mutagen-agents.tar.gz (each Windows machine)
