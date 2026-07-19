@echo off
chcp 65001 >nul
title Mutagen Web Agent - 管理

:MENU
cls
echo ========================================
echo  Mutagen Web Agent - 管理
echo ========================================
echo.
echo 1) 启动服务
echo 2) 停止服务
echo 3) 重启服务
echo 4) 查看状态
echo 5) 查看日志
echo 6) 退出
echo.
set /p opt=请选择:

if "%opt%"=="1" goto start
if "%opt%"=="2" goto stop
if "%opt%"=="3" goto restart
if "%opt%"=="4" goto status
if "%opt%"=="5" goto log
exit /b

:start
sc start MutagenWebAgent 2>nul
schtasks /run /tn MutagenWebAgent 2>nul
echo 启动命令已发送
goto end

:stop
sc stop MutagenWebAgent 2>nul
taskkill /F /IM mutagen-web-agent.exe 2>nul
echo 已停止
goto end

:restart
sc stop MutagenWebAgent 2>nul
taskkill /F /IM mutagen-web-agent.exe 2>nul
timeout /t 2 /nobreak >nul
sc start MutagenWebAgent 2>nul
schtasks /run /tn MutagenWebAgent 2>nul
echo 已重启
goto end

:status
sc query MutagenWebAgent 2>nul
schtasks /query /tn MutagenWebAgent 2>nul
goto end

:log
if exist C:\mutagen\agent.log (
    type C:\mutagen\agent.log
) else (
    echo 日志文件不存在
)
goto end

:end
echo.
pause
goto MENU
