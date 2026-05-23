package main

import (
	"errors"
	"image"
	"image/color"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
	"golang.org/x/image/draw"
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
	updateIconFunc := func(dpiFunc func(dpi int) *walk.Icon) {
		_ = ni.SetIcon(dpiFunc(ni.DPI()))
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
				updateIconFunc(iconProvider.StoppedIcon)
			})
		},
		runningCb: func(stop func()) {
			app.Synchronize(func() {
				procStopFunc = stop
				updateContextMenuFunc(false, true)
				updateTooltipFunc("running")
				updateIconFunc(iconProvider.RunningIcon)
			})
		},
		stoppedCb: func(err error) {
			app.Synchronize(func() {
				procStopFunc = func() {}
				tooltip, iconForDPI := "stopped", iconProvider.StoppedIcon

				if !stopping {
					if err != nil {
						tooltip, iconForDPI = "failed", iconProvider.FailedIcon
						_ = ni.ShowWarning("App failed", "App failed unexpectedly: "+err.Error())
					} else {
						_ = ni.ShowWarning("App exited", "App exited unexpectedly")
					}
				}
				stopping = false

				updateContextMenuFunc(true, false)
				updateTooltipFunc(tooltip)
				updateIconFunc(iconForDPI)
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
	baseIcon *walk.Icon

	stoppedOverlayLoader *LazyIconLoader
	stoppedIconDrawer    func(dpi int) *walk.Icon
	stoppedIconForDPI    map[int]*walk.Icon

	failedOverlayLoader *LazyIconLoader
	failedIconDrawer    func(dpi int) *walk.Icon
	failedIconForDPI    map[int]*walk.Icon

	disposeOwnedIconsFns []func()
}

func NewIconProvider(baseIcon *walk.Icon) *IconProvider {
	rgbaFromBitmapFunc := func(f func() (*walk.Bitmap, error)) (*image.RGBA, error) {
		icon, err := f()
		if err != nil {
			return nil, err
		}
		defer icon.Dispose()

		rgba, err := icon.ToImage()
		if err != nil {
			return nil, err
		}

		return rgba, nil
	}

	compositeIconFunc := func(baseIcon *walk.Icon, ovlLoader *LazyIconLoader, dpi int) (*walk.Icon, error) {
		dpiScaledSize := walk.SizeFrom96DPI(walk.Size{16, 16}, dpi)

		baseRgba, err := rgbaFromBitmapFunc(func() (*walk.Bitmap, error) {
			return walk.NewBitmapFromIconForDPI(baseIcon, dpiScaledSize, dpi)
		})
		if err != nil {
			return nil, err
		}

		if ovl := ovlLoader.Load(); ovl != nil {
			ovlRgba, err := rgbaFromBitmapFunc(func() (*walk.Bitmap, error) {
				return walk.NewBitmapFromIconForDPI(ovl, dpiScaledSize, dpi)
			})
			if err != nil {
				return nil, err
			}

			bounds := baseRgba.Bounds()
			bounds.Min = bounds.Min.Add(image.Point{
				int((1 - 0.625) * float64(bounds.Dx())),
				int((1 - 0.625) * float64(bounds.Dy())),
			})

			// Looks shit on 100% scale, fix it
			if dpi == 96 {
				bounds.Min.X += 1
				bounds.Min.Y += 1
			}

			draw.ApproxBiLinear.Scale(baseRgba, bounds, ovlRgba, ovlRgba.Bounds(), draw.Over, nil)
		}

		// Convert pixels to non-premultiplied and correct channel order
		for x := baseRgba.Bounds().Min.X; x < baseRgba.Bounds().Max.X; x++ {
			for y := baseRgba.Bounds().Min.Y; y < baseRgba.Bounds().Max.Y; y++ {
				npCol := color.NRGBAModel.Convert(baseRgba.RGBAAt(x, y)).(color.NRGBA)
				baseRgba.SetRGBA(x, y, color.RGBA{npCol.B, npCol.G, npCol.R, npCol.A})
			}
		}

		var bi win.BITMAPV5HEADER
		bi.BiSize = uint32(unsafe.Sizeof(bi))
		bi.BiWidth = int32(dpiScaledSize.Width)
		bi.BiHeight = int32(-dpiScaledSize.Height)
		bi.BiPlanes = 1
		bi.BiBitCount = 32
		bi.BiCompression = win.BI_RGB
		bi.BV4RedMask = 0x00FF0000
		bi.BV4GreenMask = 0x0000FF00
		bi.BV4BlueMask = 0x000000FF
		bi.BV4AlphaMask = 0xFF000000

		hdcMem := win.CreateCompatibleDC(0)
		if hdcMem == 0 {
			return nil, errors.New("CreateCompatibleDC")
		}
		defer win.DeleteDC(hdcMem)

		var lpBits unsafe.Pointer

		hbmColor := win.CreateDIBSection(hdcMem, &bi.BITMAPINFOHEADER, win.DIB_RGB_COLORS, &lpBits, 0, 0)
		if hbmColor == 0 || hbmColor == win.ERROR_INVALID_PARAMETER {
			return nil, errors.New("CreateDIBSection")
		}
		defer win.DeleteObject(win.HGDIOBJ(hbmColor))

		hbmMask := win.CreateBitmap(int32(dpiScaledSize.Width), int32(dpiScaledSize.Height), 1, 1, nil)
		if hbmMask == 0 {
			return nil, errors.New("CreateBitmap")
		}
		defer win.DeleteObject(win.HGDIOBJ(hbmMask))

		dst := (*[1 << 24]byte)(lpBits)
		copy(dst[:], baseRgba.Pix)

		var ii win.ICONINFO
		ii.FIcon = win.TRUE
		ii.HbmMask = hbmMask
		ii.HbmColor = hbmColor

		return walk.NewIconFromHICONForDPI(win.CreateIconIndirect(&ii), dpi)
	}

	stoppedOverlayLoader := NewLazyIconLoader(func() *walk.Icon {
		icon, _ := walk.NewIconFromSysDLLWithSize("imageres", -1403, 15)
		return icon
	})

	stoppedIconDrawer := func(dpi int) *walk.Icon {
		icon, _ := compositeIconFunc(baseIcon, stoppedOverlayLoader, dpi)
		return icon
	}

	failedOverlayLoader := NewLazyIconLoader(func() *walk.Icon {
		icon, _ := walk.NewIconFromSysDLLWithSize("imageres", -1402, 15)
		return icon
	})

	failedIconDrawer := func(dpi int) *walk.Icon {
		icon, _ := compositeIconFunc(baseIcon, failedOverlayLoader, dpi)
		return icon
	}

	return &IconProvider{
		baseIcon:             baseIcon,
		stoppedOverlayLoader: stoppedOverlayLoader,
		stoppedIconDrawer:    stoppedIconDrawer,
		failedOverlayLoader:  failedOverlayLoader,
		failedIconDrawer:     failedIconDrawer,
	}
}

func (p *IconProvider) RunningIcon(dpi int) *walk.Icon {
	return p.baseIcon
}

func (p *IconProvider) StoppedIcon(dpi int) *walk.Icon {
	if p.stoppedIconForDPI == nil {
		p.stoppedIconForDPI = make(map[int]*walk.Icon)
	}
	return p.cacheIcon(p.stoppedIconForDPI, dpi, p.stoppedIconDrawer)
}

func (p *IconProvider) FailedIcon(dpi int) *walk.Icon {
	if p.failedIconForDPI == nil {
		p.failedIconForDPI = make(map[int]*walk.Icon)
	}
	return p.cacheIcon(p.failedIconForDPI, dpi, p.failedIconDrawer)
}

func (p *IconProvider) cacheIcon(cacheMap map[int]*walk.Icon, dpi int, drawerFunc func(dpi int) *walk.Icon) (icon *walk.Icon) {
	if icon = cacheMap[dpi]; icon != nil {
		return
	}

	icon = drawerFunc(dpi)
	if icon == nil {
		icon = p.baseIcon
	} else {
		p.disposeOwnedIconsFns = append(p.disposeOwnedIconsFns, icon.Dispose)
	}

	cacheMap[dpi] = icon
	return icon
}

func (p IconProvider) Dispose() {
	p.stoppedOverlayLoader.Dispose()
	p.failedOverlayLoader.Dispose()

	for _, disposeFn := range p.disposeOwnedIconsFns {
		disposeFn()
	}
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
