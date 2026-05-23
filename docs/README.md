# trayprog

A minimal Windows system tray wrapper for command-line programs, configured via an `.ini` file.

## Configuration

Configuration file is named after the `.exe`, that is, if you save it as `myname.exe`, then the configuration file is `myname.ini`.

```ini
cmd   = ping -t 127.0.0.1  ; command to run, required
title = Ping               ; display name
icon  = trayprog.ico       ; icon file with optional index, e.g. shell32.dll,3
```

If you do not configure an icon, it would be attempted to load from an `.ico` file named after the `.exe`, similar to the configuration.

## Building

Requires Go 1.26+ and Windows.

```bat
build.bat
```
