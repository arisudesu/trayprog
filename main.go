package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/tailscale/walk"
	"golang.org/x/sys/windows"
	"gopkg.in/ini.v1"
)

func main() {
	exePath, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}

	iniPath := strings.TrimSuffix(exePath, filepath.Ext(exePath)) + ".ini"
	icoPath := strings.TrimSuffix(exePath, filepath.Ext(exePath)) + ".ico"

	app, err := walk.InitApp()
	if err != nil {
		log.Fatal(err)
	}

	var config struct {
		Cmd   string `ini:"cmd"`
		Title string `ini:"title"`
		Icon  string `ini:"icon"`
	}
	if err := ini.MapTo(&config, iniPath); err != nil {
		log.Fatal(err)
	}

	cmdArgv, err := windows.DecomposeCommandLine(config.Cmd)
	if err != nil {
		log.Fatal(err)
	}

	if len(cmdArgv) == 0 {
		log.Fatal("missing cmd")
	}

	// Defaults
	if config.Title == "" {
		config.Title = cmdArgv[0]
	}
	if config.Icon == "" {
		config.Icon = icoPath
	}

	iconFile, iconIdxStr, _ := strings.Cut(config.Icon, ",")
	iconIdx, _ := strconv.Atoi(iconIdxStr)

	// Mandatory icon, can't work without it
	iconBase, _ := walk.NewIconExtractedFromFileWithSize(iconFile, iconIdx, 16)
	if iconBase == nil {
		iconBase, err = walk.NewIconFromSysDLLWithSize("imageres", -5323, 16)
	}
	if err != nil {
		log.Fatal(err)
	}
	defer iconBase.Dispose()

	// Optional icons, tried only once, and may fail to load
	iconProvider := NewIconProvider(iconBase)
	defer iconProvider.Dispose()
	iconStopped, iconFailed := iconProvider.StoppedIcon(), iconProvider.FailedIcon()

	ni, err := walk.NewNotifyIcon()
	if err != nil {
		log.Fatal(err)
	}
	defer ni.Dispose()

	actionStrt := walk.NewAction()
	if err := actionStrt.SetText("Start"); err != nil {
		log.Fatal(err)
	}
	actionStop := walk.NewAction()
	if err := actionStop.SetText("Stop"); err != nil {
		log.Fatal(err)
	}
	actionExit := walk.NewAction()
	if err := actionExit.SetText("E&xit"); err != nil {
		log.Fatal(err)
	}

	if err := ni.ContextMenu().Actions().Add(actionStrt); err != nil {
		log.Fatal(err)
	}
	if err := ni.ContextMenu().Actions().Add(actionStop); err != nil {
		log.Fatal(err)
	}
	if err := ni.ContextMenu().Actions().Add(walk.NewSeparatorAction()); err != nil {
		log.Fatal(err)
	}
	if err := ni.ContextMenu().Actions().Add(actionExit); err != nil {
		log.Fatal(err)
	}

	if err := ni.SetVisible(true); err != nil {
		log.Fatal(err)
	}

	updateContextMenuFunc := func(canStart, canStop bool) {
		_ = actionStrt.SetEnabled(canStart)
		_ = actionStop.SetEnabled(canStop)
	}
	updateTooltipFunc := func(text string) {
		_ = ni.SetToolTip(config.Title + " - " + text)
	}
	updateIconFunc := func(icon walk.Image) {
		_ = ni.SetIcon(icon)
	}

	var stopping bool // skips notification
	procStopFunc := func() {}

	cbs := runCbs{
		// State tracking happens on main thread, hence
		// all updates are in "synchronized" blocks
		startingCb: func() {
			app.Synchronize(func() {
				procStopFunc = func() {}
				updateContextMenuFunc(false, false)
				updateTooltipFunc("starting")
				updateIconFunc(iconStopped)
			})
		},
		runningCb: func(stop func()) {
			app.Synchronize(func() {
				procStopFunc = stop
				updateContextMenuFunc(false, true)
				updateTooltipFunc("running")
				updateIconFunc(iconBase)
			})
		},
		stoppedCb: func(err error) {
			app.Synchronize(func() {
				procStopFunc = func() {}
				tooltip, icon := "stopped", iconStopped

				if !stopping {
					if err != nil {
						tooltip, icon = "failed", iconFailed
						_ = ni.ShowWarning("App failed", "App failed unexpectedly: "+err.Error())
					} else {
						_ = ni.ShowWarning("App exited", "App exited unexpectedly")
					}
				}
				stopping = false

				updateContextMenuFunc(true, false)
				updateTooltipFunc(tooltip)
				updateIconFunc(icon)
			})
		},
	}

	strtFunc := func() {
		go run(cmdArgv, cbs)
	}
	stopFunc := func() {
		stopping = true
		procStopFunc()
	}
	exitFunc := func() {
		app.Exit(0)
	}

	defer stopFunc()

	actionStrt.Triggered().Attach(strtFunc)
	actionStop.Triggered().Attach(stopFunc)
	actionExit.Triggered().Attach(exitFunc)

	app.Synchronize(strtFunc)

	app.Run()
}

type IconProvider struct {
	stoppedOverlayLoader *LazyIconLoader
	stoppedIcon          walk.Image
	failedOverlayLoader  *LazyIconLoader
	failedIcon           walk.Image
}

func NewIconProvider(baseIcon *walk.Icon) *IconProvider {
	drawOverlayScaledFunc := func(overlay *walk.Icon, canvas *walk.Canvas, bounds walk.Rectangle) error {
		ovw := int(float64(bounds.Width) * 0.625)
		ovh := int(float64(bounds.Height) * 0.625)

		bounds = walk.Rectangle{bounds.Width - ovw, bounds.Height - ovh, ovw, ovh}

		// Looks shit on 100% scale, fix it
		if canvas.DPI() == 96 {
			bounds.X += 1
			bounds.Y += 1
		}

		return canvas.DrawImageStretchedPixels(overlay, bounds)
	}

	stoppedOverlayLoader := NewLazyIconLoader(func() *walk.Icon {
		icon, _ := walk.NewIconFromSysDLLWithSize("imageres", -1403, 15)
		return icon
	})

	stoppedIcon := walk.NewPaintFuncImage(walk.Size{16, 16}, func(canvas *walk.Canvas, bounds walk.Rectangle) error {
		bounds = walk.RectangleFrom96DPI(bounds, canvas.DPI())

		if err := canvas.DrawImageStretchedPixels(baseIcon, bounds); err != nil {
			return err
		}
		if ovl := stoppedOverlayLoader.Load(); ovl != nil {
			return drawOverlayScaledFunc(ovl, canvas, bounds)
		}
		return nil
	})

	failedOverlayLoader := NewLazyIconLoader(func() *walk.Icon {
		icon, _ := walk.NewIconFromSysDLLWithSize("imageres", -1402, 15)
		return icon
	})

	failedIcon := walk.NewPaintFuncImage(walk.Size{16, 16}, func(canvas *walk.Canvas, bounds walk.Rectangle) error {
		bounds = walk.RectangleFrom96DPI(bounds, canvas.DPI())

		if err := canvas.DrawImageStretchedPixels(baseIcon, bounds); err != nil {
			return err
		}
		if ovl := failedOverlayLoader.Load(); ovl != nil {
			return drawOverlayScaledFunc(ovl, canvas, bounds)
		}
		return nil
	})

	return &IconProvider{
		stoppedOverlayLoader: stoppedOverlayLoader,
		stoppedIcon:          stoppedIcon,
		failedOverlayLoader:  failedOverlayLoader,
		failedIcon:           failedIcon,
	}
}

func (p IconProvider) StoppedIcon() walk.Image {
	return p.stoppedIcon
}

func (p IconProvider) FailedIcon() walk.Image {
	return p.failedIcon
}

func (p IconProvider) Dispose() {
	p.stoppedOverlayLoader.Dispose()
	p.stoppedIcon.Dispose()
	p.failedOverlayLoader.Dispose()
	p.failedIcon.Dispose()
}

type LazyIconLoader struct {
	load func() *walk.Icon
	done bool
	icon *walk.Icon
}

func NewLazyIconLoader(load func() *walk.Icon) *LazyIconLoader {
	return &LazyIconLoader{load: load}
}

func (l *LazyIconLoader) Load() *walk.Icon {
	if !l.done {
		l.done = true
		l.icon = l.load()
	}
	return l.icon
}

func (l *LazyIconLoader) Dispose() {
	if l.icon != nil {
		l.icon.Dispose()
	}
}

type runCbs struct {
	startingCb func()
	runningCb  func(stop func())
	stoppedCb  func(err error)
}

func run(argv []string, cfg runCbs) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cfg.startingCb()

	if err := cmd.Start(); err != nil {
		cfg.stoppedCb(err)
		return
	}

	cfg.runningCb(func() { cmd.Process.Kill() })

	cfg.stoppedCb(cmd.Wait())
}
