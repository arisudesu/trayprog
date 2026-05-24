//go:build windows

package main

import (
	"errors"
	"log"
	"runtime"

	"github.com/tailscale/walk"
	"golang.org/x/sys/windows"

	"trayawake/sys"
)

func init() {
	runtime.LockOSThread()
}

func main() {
	mutexName, err := windows.UTF16PtrFromString("TrayAwakeMutex")
	if err != nil {
		log.Fatal(err)
	}

	mutex, err := windows.CreateMutex(nil, false, mutexName)
	if err != nil {
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return
		}
		log.Fatal(err)
	}
	defer windows.CloseHandle(mutex)

	app, err := walk.InitApp()
	if err != nil {
		log.Fatal(err)
	}

	icon, err := walk.NewIconFromResourceIdWithSize(2, walk.Size{16, 16})
	if err != nil {
		log.Fatal(err)
	}
	defer icon.Dispose()

	ni, err := walk.NewNotifyIcon()
	if err != nil {
		log.Fatal(err)
	}
	defer ni.Dispose()

	if err := ni.SetIcon(icon); err != nil {
		log.Fatal(err)
	}
	if err := ni.SetToolTip("TrayAwake"); err != nil {
		log.Fatal(err)
	}

	actionExit := walk.NewAction()
	if err := actionExit.SetText("E&xit"); err != nil {
		log.Fatal(err)
	}
	if err := ni.ContextMenu().Actions().Add(actionExit); err != nil {
		log.Fatal(err)
	}
	actionExit.Triggered().Attach(func() {
		app.Exit(0)
	})

	if err := ni.SetVisible(true); err != nil {
		log.Fatal(err)
	}

	sys.SetThreadExecutionState(sys.ES_CONTINUOUS | sys.ES_DISPLAY_REQUIRED)
	defer sys.SetThreadExecutionState(sys.ES_CONTINUOUS)

	app.Run()
}
