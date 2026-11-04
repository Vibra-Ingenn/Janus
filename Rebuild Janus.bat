@echo off
setlocal
cd /d "%~dp0"
echo Rebuild Janus — closing only dist\janus.exe from this folder...
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\stop_dist_janus.ps1"
if errorlevel 1 (
  echo Stop step failed.
  pause
  exit /b 1
)
call powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build.ps1" -SkipDownload
if errorlevel 1 (
  echo Build FAILED.
  pause
  exit /b 1
)
for /f "delims=" %%a in ('powershell -NoProfile -Command "$a=$env:JANUS_LISTEN_ADDR; if(-not $a){$a='127.0.0.1:8990'}; if($a -notmatch ':'){$a='127.0.0.1:'+$a}; Write-Output ('http://'+$a+'/')"') do set "JANUS_URL=%%a"
echo Starting dist\janus.exe ...
start "" "%~dp0dist\janus.exe"
timeout /t 3 /nobreak >nul
start "" "%JANUS_URL%"
echo Done. Open %JANUS_URL%
pause
