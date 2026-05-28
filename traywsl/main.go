//go:build windows

package main

import (
	"bufio"
	"bytes"
	"errors"
	"log"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/tailscale/walk"
	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func main() {
	mutexName, err := windows.UTF16PtrFromString("TrayWSLMutex")
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

	actions := ni.ContextMenu().Actions()

	actionShdn := walk.NewAction()
	if err := actionShdn.SetText("Shutdown WSL"); err != nil {
		log.Fatal(err)
	}
	actionExit := walk.NewAction()
	if err := actionExit.SetText("E&xit"); err != nil {
		log.Fatal(err)
	}

	if err := actions.Add(actionShdn); err != nil {
		log.Fatal(err)
	}
	if err := actions.Add(actionExit); err != nil {
		log.Fatal(err)
	}

	if err := ni.SetIcon(icon); err != nil {
		log.Fatal(err)
	}

	var runningDistros []string
	pollFaster := make(chan struct{}, 1)

	triggerPollFaster := func() {
		select {
		case pollFaster <- struct{}{}:
		default:
		}
	}

	onChange := func(distros []string) {
		app.Synchronize(func() {
			_ = ni.SetVisible(len(distros) > 0)
			_ = ni.SetToolTip(strings.Join(append([]string{"Running distributions:"}, distros...), "\n -"))

			runningDistros = runningDistros[:0]
			runningDistros = append(runningDistros, distros...)
		})
	}

	updateContextMenu := func() {
		for range actions.Len() - 2 {
			_ = actions.RemoveAt(0)
		}

		for i, distro := range runningDistros {
			actionDistroName := walk.NewAction()
			_ = actionDistroName.SetText(distro)
			_ = actionDistroName.SetEnabled(false)

			actionOpenTerminal := walk.NewAction()
			_ = actionOpenTerminal.SetText("Open terminal")
			actionOpenTerminal.Triggered().Attach(func() {
				openTerminal(distro)
			})

			actionTerminate := walk.NewAction()
			_ = actionTerminate.SetText("Terminate")
			actionTerminate.Triggered().Attach(func() {
				terminate(distro)
				triggerPollFaster()
			})

			topOffset := i * 4

			_ = actions.Insert(topOffset+0, actionDistroName)
			_ = actions.Insert(topOffset+1, actionOpenTerminal)
			_ = actions.Insert(topOffset+2, actionTerminate)
			_ = actions.Insert(topOffset+3, walk.NewSeparatorAction())
		}
	}

	ni.ShowingContextMenu().Attach(func() bool {
		updateContextMenu()
		return true
	})
	actionShdn.Triggered().Attach(func() {
		shutdown()
		triggerPollFaster()
	})
	actionExit.Triggered().Attach(func() {
		app.Exit(0)
	})

	app.Synchronize(func() {
		go poll(onChange, pollFaster)
	})

	app.Run()
}

func poll(onChange func([]string), pollFast <-chan struct{}) {
	const (
		intervalSlow = 1 * time.Second
		intervalFast = 250 * time.Millisecond
		fastDuration = 2 * time.Second
	)

	interval := intervalSlow
	var fastUntil time.Time

	var lastDistros []string
	for {
		distros := listRunning()
		if slices.Compare(distros, lastDistros) != 0 {
			onChange(distros)
			lastDistros = lastDistros[:0]
			lastDistros = append(lastDistros, distros...)
		}

		if time.Now().After(fastUntil) {
			interval = intervalSlow
		}

		select {
		case <-time.After(interval):
		case <-pollFast:
			interval = intervalFast
			fastUntil = time.Now().Add(fastDuration)
		}
	}
}

var utf16 = unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM)

func listRunning() []string {
	cmd := exec.Command("wsl.exe", "--list", "--running", "--quiet")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	var distros []string

	decoder := utf16.NewDecoder()
	scanner := bufio.NewScanner(
		transform.NewReader(
			bytes.NewReader(output), decoder))

	for scanner.Scan() {
		distros = append(distros, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil
	}

	slices.Sort(distros)
	return slices.Clip(distros)
}

func openTerminal(distro string) {
	distro, _, _ = strings.Cut(distro, " ")
	if distro == "" {
		return
	}

	executeWSLCommand("--distribution "+distro+" --cd ~", true)
}

func terminate(distro string) {
	distro, _, _ = strings.Cut(distro, " ")
	if distro == "" {
		return
	}

	executeWSLCommand("--terminate "+distro, false)
}

func shutdown() {
	executeWSLCommand("--shutdown", false)
}

func executeWSLCommand(args string, visible bool) {
	cmdLine, err := windows.UTF16PtrFromString("wsl.exe " + args)
	if err != nil {
		return
	}

	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESHOWWINDOW
	if visible {
		si.ShowWindow = windows.SW_SHOWNORMAL
	} else {
		si.ShowWindow = windows.SW_HIDE
	}

	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, cmdLine, nil, nil, false, 0, nil, nil, &si, &pi); err != nil {
		return
	}
	_ = windows.CloseHandle(pi.Thread)
	_ = windows.CloseHandle(pi.Process)
}