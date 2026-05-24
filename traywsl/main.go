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

	updateContextMenu := func() error {
		for range actions.Len() - 2 {
			if err := actions.RemoveAt(0); err != nil {
				return err
			}
		}

		for i, distro := range runningDistros {
			actionDistroName := walk.NewAction()
			if err := actionDistroName.SetText(distro); err != nil {
				return err
			}
			if err := actionDistroName.SetEnabled(false); err != nil {
				return err
			}

			actionOpenTerminal := walk.NewAction()
			if err := actionOpenTerminal.SetText("Open terminal"); err != nil {
				return err
			}
			actionOpenTerminal.Triggered().Attach(func() {
				openTerminal(distro)
			})

			actionTerminate := walk.NewAction()
			if err := actionTerminate.SetText("Terminate"); err != nil {
				return err
			}
			actionTerminate.Triggered().Attach(func() {
				terminate(distro)
				triggerPollFaster()
			})

			topOffset := i * 4

			if err := actions.Insert(topOffset+0, actionDistroName); err != nil {
				return err
			}
			if err := actions.Insert(topOffset+1, actionOpenTerminal); err != nil {
				return err
			}
			if err := actions.Insert(topOffset+2, actionTerminate); err != nil {
				return err
			}
			if err := actions.Insert(topOffset+3, walk.NewSeparatorAction()); err != nil {
				return err
			}
		}
		return nil
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

	action, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return
	}

	exe, err := windows.UTF16PtrFromString("wsl.exe")
	if err != nil {
		return
	}

	exeArgs, err := windows.UTF16PtrFromString("--distribution " + distro + " --cd ~")
	if err != nil {
		return
	}

	windows.ShellExecute(windows.Handle(0), action, exe, exeArgs, nil, windows.SW_SHOWNORMAL)
}

func terminate(distro string) {
	distro, _, _ = strings.Cut(distro, " ")
	if distro == "" {
		return
	}

	action, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return
	}

	exe, err := windows.UTF16PtrFromString("wsl.exe")
	if err != nil {
		return
	}

	exeArgs, err := windows.UTF16PtrFromString("--terminate " + distro)
	if err != nil {
		return
	}

	windows.ShellExecute(windows.Handle(0), action, exe, exeArgs, nil, windows.SW_HIDE)
}

func shutdown() {
	action, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return
	}

	exe, err := windows.UTF16PtrFromString("wsl.exe")
	if err != nil {
		return
	}

	exeArgs, err := windows.UTF16PtrFromString("--shutdown")
	if err != nil {
		return
	}

	windows.ShellExecute(windows.Handle(0), action, exe, exeArgs, nil, windows.SW_HIDE)
}
