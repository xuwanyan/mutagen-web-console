@echo off
setlocal
set ROOT=%~dp0
set BUILD=%ROOT%build

echo === Building server.exe (windows) ===
cd /d "%ROOT%server"
go build -o "%BUILD%server.exe" .
if %ERRORLEVEL% neq 0 exit /b %ERRORLEVEL%

echo === Building server_linux ===
cd /d "%ROOT%server"
set GOOS=linux
set GOARCH=amd64
go build -a -o "%BUILD%server_linux" .
if %ERRORLEVEL% neq 0 exit /b %ERRORLEVEL%

echo === Building agent.exe ===
cd /d "%ROOT%agent"
go build -o "%BUILD%agent.exe" .
if %ERRORLEVEL% neq 0 exit /b %ERRORLEVEL%

echo === Building frontend ===
cd /d "%ROOT%web"
call npm run build
if %ERRORLEVEL% neq 0 exit /b %ERRORLEVEL%

echo === Copying frontend dist ===
xcopy /E /Y "%ROOT%web\dist\*" "%BUILD%web\"
if %ERRORLEVEL% neq 0 exit /b %ERRORLEVEL%

echo.
echo ============================================
echo Build complete! Artifacts in %BUILD%:
echo   server.exe     - Windows server
echo   server_linux   - Linux server
echo   agent.exe      - Windows agent
echo   web\           - Frontend static files
echo ============================================
endlocal
