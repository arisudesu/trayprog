@echo off

set BUILDDIR=%~dp0
cd /d %BUILDDIR% || exit /b 1

:build
    echo [+] Compiling app manifest
    go tool rsrc -manifest trayprog.exe.manifest || goto :error
    echo [+] Compiling executable
    go build -ldflags="-H windowsgui -s -w" -trimpath -buildvcs=false -v -o "trayprog.exe" || goto :error
    goto :eof

:error
	echo [-] Failed with error #%errorlevel%.
	cmd /c exit %errorlevel%
