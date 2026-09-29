@echo off
setlocal

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1"
set "BUILD_EXIT_CODE=%ERRORLEVEL%"
if not "%BUILD_EXIT_CODE%"=="0" goto build_failed

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-audio.ps1"
set "BUILD_EXIT_CODE=%ERRORLEVEL%"
if not "%BUILD_EXIT_CODE%"=="0" goto build_failed

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0sync-setup.ps1"
set "BUILD_EXIT_CODE=%ERRORLEVEL%"
if not "%BUILD_EXIT_CODE%"=="0" goto build_failed

powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0zip-setup.ps1"
set "BUILD_EXIT_CODE=%ERRORLEVEL%"
if not "%BUILD_EXIT_CODE%"=="0" goto build_failed

echo.
echo Build completed successfully.
pause
exit /b 0

:build_failed
echo.
echo Build failed with exit code %BUILD_EXIT_CODE%.
pause
exit /b %BUILD_EXIT_CODE%
