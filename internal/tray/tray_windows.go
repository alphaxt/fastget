//go:build windows

package tray

import (
	"syscall"
	"unsafe"
)

// Win32 bindings via the standard library only (no cgo, no external modules).
var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pRegisterClassEx  = user32.NewProc("RegisterClassExW")
	pCreateWindowEx   = user32.NewProc("CreateWindowExW")
	pDefWindowProc    = user32.NewProc("DefWindowProcW")
	pGetMessage       = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessage  = user32.NewProc("DispatchMessageW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pDestroyWindow    = user32.NewProc("DestroyWindow")
	pLoadImage        = user32.NewProc("LoadImageW")
	pCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	pAppendMenu       = user32.NewProc("AppendMenuW")
	pTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	pGetCursorPos     = user32.NewProc("GetCursorPos")
	pSetForegroundWin = user32.NewProc("SetForegroundWindow")
	pPostMessage      = user32.NewProc("PostMessageW")

	pShellNotifyIcon = shell32.NewProc("Shell_NotifyIconW")

	pGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy      = 0x0002
	wmClose        = 0x0010
	wmCommand      = 0x0111
	wmApp          = 0x8000
	wmTrayCallback = wmApp + 1

	wmRBUTTONUP = 0x0205
	wmLBUTTONUP = 0x0202

	nimAdd     = 0x00000000
	nimDelete  = 0x00000002
	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	imageIcon      = 1
	lrDefaultSize  = 0x00000040
	lrShared       = 0x00008000
	idiApplication = 32512

	mfString = 0x00000000

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	cmdOpen = 1
	cmdQuit = 2
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type point struct{ X, Y int32 }

type msg struct {
	hwnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

// notifyIconData mirrors the Win32 NOTIFYICONDATA (W) structure. We keep only
// the fields through szTip, which is all nifMessage|nifIcon|nifTip needs.
type notifyIconData struct {
	cbSize           uint32
	hWnd             syscall.Handle
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            syscall.Handle
	szTip            [128]uint16
}

// run implements the Windows tray. Any failure falls back to waiting on the
// context so the app keeps running headless rather than crashing.
func run(opts Options) error {
	hInstance, _, _ := pGetModuleHandle.Call(0)

	className, _ := syscall.UTF16PtrFromString("fastgetTrayClass")

	var hwnd syscall.Handle
	// Window procedure: handle the tray callback and menu commands.
	wndProc := syscall.NewCallback(func(h syscall.Handle, message uint32, wParam, lParam uintptr) uintptr {
		switch message {
		case wmTrayCallback:
			// lParam low word is the mouse event.
			ev := uint32(lParam & 0xffff)
			if ev == wmRBUTTONUP || ev == wmLBUTTONUP {
				showMenu(h, opts)
			}
			return 0
		case wmCommand:
			switch wParam & 0xffff {
			case cmdOpen:
				if opts.OnOpen != nil {
					go opts.OnOpen()
				}
			case cmdQuit:
				pPostMessage.Call(uintptr(h), wmClose, 0, 0)
			}
			return 0
		case wmClose:
			pDestroyWindow.Call(uintptr(h))
			return 0
		case wmDestroy:
			pPostQuitMessage.Call(0)
			return 0
		}
		ret, _, _ := pDefWindowProc.Call(uintptr(h), uintptr(message), wParam, lParam)
		return ret
	})

	wc := wndClassEx{
		lpfnWndProc:   wndProc,
		hInstance:     syscall.Handle(hInstance),
		lpszClassName: className,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	if ret, _, _ := pRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); ret == 0 {
		return fallbackWait(opts)
	}

	// Message-only window (hidden): parent = HWND_MESSAGE (-3).
	h, _, _ := pCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0,
		^uintptr(2), // HWND_MESSAGE = (HWND)-3
		0,
		hInstance,
		0,
	)
	if h == 0 {
		return fallbackWait(opts)
	}
	hwnd = syscall.Handle(h)

	// Load the default application icon (always available).
	hIcon, _, _ := pLoadImage.Call(0, uintptr(idiApplication), imageIcon, 0, 0, lrShared|lrDefaultSize)

	nid := notifyIconData{
		hWnd:             hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmTrayCallback,
		hIcon:            syscall.Handle(hIcon),
	}
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	copyTip(&nid, opts.Title)

	if ret, _, _ := pShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); ret == 0 {
		pDestroyWindow.Call(uintptr(hwnd))
		return fallbackWait(opts)
	}

	// Close the window when the context is cancelled.
	go func() {
		<-opts.Context.Done()
		pPostMessage.Call(uintptr(hwnd), wmClose, 0, 0)
	}()

	// Standard Win32 message loop.
	var m msg
	for {
		ret, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 { // 0 = WM_QUIT, -1 = error
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}

	// Remove the icon and fire OnQuit.
	pShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	if opts.OnQuit != nil {
		opts.OnQuit()
	}
	return nil
}

// showMenu pops up the right-click context menu at the cursor.
func showMenu(hwnd syscall.Handle, opts Options) {
	hMenu, _, _ := pCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	openText, _ := syscall.UTF16PtrFromString("Open fastget")
	quitText, _ := syscall.UTF16PtrFromString("Quit")
	pAppendMenu.Call(hMenu, mfString, cmdOpen, uintptr(unsafe.Pointer(openText)))
	pAppendMenu.Call(hMenu, mfString, cmdQuit, uintptr(unsafe.Pointer(quitText)))

	var pt point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// Required so the menu dismisses correctly when clicking elsewhere.
	pSetForegroundWin.Call(uintptr(hwnd))

	cmd, _, _ := pTrackPopupMenu.Call(
		hMenu,
		tpmRightButton|tpmReturnCmd,
		uintptr(pt.X), uintptr(pt.Y),
		0, uintptr(hwnd), 0,
	)
	// Route the chosen command back through WM_COMMAND handling.
	if cmd != 0 {
		pPostMessage.Call(uintptr(hwnd), wmCommand, cmd&0xffff, 0)
	}
}

// copyTip writes the tooltip text into the fixed-size szTip buffer.
func copyTip(nid *notifyIconData, s string) {
	if s == "" {
		s = "fastget"
	}
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	for i := 0; i < len(u) && i < len(nid.szTip)-1; i++ {
		nid.szTip[i] = u[i]
	}
}

// fallbackWait keeps the process alive (headless) when the tray cannot be shown.
func fallbackWait(opts Options) error {
	<-opts.Context.Done()
	if opts.OnQuit != nil {
		opts.OnQuit()
	}
	return nil
}
