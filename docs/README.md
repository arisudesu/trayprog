# trayprog

A minimal Windows system tray wrapper for command-line programs, configured via an `.ini` file.

## Configuration

The executable can be renamed freely — the `.ini` and `.ico` files are resolved by matching its name.

Create a `.ini` file next to the `.exe` with the same name (e.g. if you keep the original name `trayprog.exe`, then `trayprog.ini` is loaded):

```ini
cmd   = ping -t 127.0.0.1  ; command to run, required
title = Ping               ; display name
icon  = trayprog.ico       ; icon file with optional index, e.g. shell32.dll,3
```

If `icon` is not set, the icon is loaded from a file next to the `.exe` with the same name (e.g. `trayprog.ico`, similar to how `trayprog.ini` is found).

## Building

Requires Go 1.26+ and Windows.

```bat
build.bat
```
