@echo off
chcp 65001 >nul
cd /d "%~dp0"
agent\agent.exe -config=agent-config.json
