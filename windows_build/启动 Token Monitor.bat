@echo off
chcp 65001 >nul 2>&1
title Token Monitor

REM 检查 Python 是否安装
where python >nul 2>&1
if %errorlevel% neq 0 (
    echo.
    echo ❌ 未检测到 Python，请先安装 Python 3.8+
    echo    下载地址: https://www.python.org/downloads/
    echo    安装时请勾选 "Add Python to PATH"
    echo.
    pause
    exit /b 1
)

REM 启动
cd /d "%~dp0"
echo 🔥 正在启动 Token Monitor...
pythonw run.pyw 2>nul || python run.pyw
