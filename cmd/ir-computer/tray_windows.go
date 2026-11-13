package main

import (
	"context"
	"errors"
	"log"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/kumoSleeping/iridium/internal/join"
)

// The tray on Windows: a notification-area icon (Shell_NotifyIconW) of a hidden window, its menu
// a popup menu at the cursor.

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassExW         = user32.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32.NewProc("CreateWindowExW")
	pDefWindowProcW           = user32.NewProc("DefWindowProcW")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pGetMessageW              = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessageW         = user32.NewProc("DispatchMessageW")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pPostQuitMessage          = user32.NewProc("PostQuitMessage")
	pCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	pAppendMenuW              = user32.NewProc("AppendMenuW")
	pTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	pDestroyMenu              = user32.NewProc("DestroyMenu")
	pGetCursorPos             = user32.NewProc("GetCursorPos")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pRegisterWindowMessageW   = user32.NewProc("RegisterWindowMessageW")
	pCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	pDestroyIcon              = user32.NewProc("DestroyIcon")
	pGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	pShellNotifyIconW         = shell32.NewProc("Shell_NotifyIconW")
	pGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmNull      = 0x0000
	wmDestroy   = 0x0002
	wmClose     = 0x0010
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205
	wmTray      = 0x8000 + 1 // WM_APP+1: the icon's mouse messages
	wmState     = 0x8000 + 2 // WM_APP+2: the join's state changed

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4

	mfString    = 0x0
	mfGrayed    = 0x1
	mfChecked   = 0x8
	mfSeparator = 0x800

	tpmRightButton = 0x2
	tpmBottomAlign = 0x20
	tpmReturnCmd   = 0x100

	smCxSmIcon = 49

	cmdOpen    = 1
	cmdAtLogin = 2
	cmdQuit    = 3
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type point struct{ X, Y int32 }

type msg struct {
	Wnd     windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

// notifyIconData is NOTIFYICONDATAW.
type notifyIconData struct {
	Size            uint32
	Wnd             windows.Handle
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            windows.Handle
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        windows.GUID
	BalloonIcon     windows.Handle
}

type winTray struct {
	t              join.Tray
	wnd            windows.Handle
	icons          [2]windows.Handle // offline, online
	nid            notifyIconData
	taskbarCreated uintptr
}

// cur is the one tray: its window procedure's.
var cur *winTray

func tray(ctx context.Context, t join.Tray) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w := &winTray{t: t}
	size, _, _ := pGetSystemMetrics.Call(smCxSmIcon)
	if size == 0 {
		size = 16
	}
	for i := range w.icons {
		b := pngOf(mark(int(size), false, i == 0), 0)
		h, _, err := pCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 1, 0x00030000, size, size, 0)
		if h == 0 {
			return err
		}
		w.icons[i] = windows.Handle(h)
		defer pDestroyIcon.Call(h)
	}
	inst, _, _ := pGetModuleHandleW.Call(0)
	name, _ := windows.UTF16PtrFromString("IridiumComputer")
	wc := wndClassEx{WndProc: windows.NewCallback(wndProc), Instance: windows.Handle(inst), ClassName: name}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return err
	}
	cur = w
	h, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, 0, 0, inst, 0)
	if h == 0 {
		return err
	}
	w.wnd = windows.Handle(h)
	taskbar, _ := windows.UTF16PtrFromString("TaskbarCreated")
	w.taskbarCreated, _, _ = pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(taskbar)))
	w.nid = notifyIconData{Wnd: w.wnd, ID: 1, Flags: nifMessage | nifIcon | nifTip, CallbackMessage: wmTray}
	w.nid.Size = uint32(unsafe.Sizeof(w.nid))
	w.show()
	// Before the taskbar is there (at logon) it fails: added once TaskbarCreated comes.
	w.notify(nimAdd)

	go func() {
		for {
			select {
			case <-ctx.Done():
				pPostMessageW.Call(uintptr(w.wnd), wmClose, 0, 0)
				return
			case <-t.Status.Changed():
				pPostMessageW.Call(uintptr(w.wnd), wmState, 0, 0)
			}
		}
	}()
	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || int32(r) == -1 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	if ctx.Err() == nil {
		return errors.New("its window ended")
	}
	return nil
}

// show sets the icon and tip from the state.
func (w *winTray) show() {
	st := w.t.Status.Get()
	w.nid.Icon = w.icons[0]
	if st.Online {
		w.nid.Icon = w.icons[1]
	}
	tip, _ := windows.UTF16FromString(line(w.t.URL, st))
	if len(tip) > len(w.nid.Tip) {
		tip = append(tip[:len(w.nid.Tip)-1], 0)
	}
	w.nid.Tip = [128]uint16{}
	copy(w.nid.Tip[:], tip)
}

func (w *winTray) notify(op uintptr) bool {
	r, _, _ := pShellNotifyIconW.Call(op, uintptr(unsafe.Pointer(&w.nid)))
	return r != 0
}

func (w *winTray) menu() {
	m, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(m)
	add := func(flags, id uintptr, s string) {
		var p *uint16
		if s != "" {
			p, _ = windows.UTF16PtrFromString(s)
		}
		pAppendMenuW.Call(m, flags, id, uintptr(unsafe.Pointer(p)))
	}
	add(mfString|mfGrayed, 0, line(w.t.URL, w.t.Status.Get()))
	add(mfString, cmdOpen, labelOpen)
	atLogin := w.t.AtLogin()
	flags := uintptr(mfString)
	if atLogin {
		flags |= mfChecked
	}
	add(flags, cmdAtLogin, labelAtLogin)
	add(mfSeparator, 0, "")
	add(mfString, cmdQuit, labelQuit)
	var p point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	// The menu closes on a click elsewhere only if its window is in front.
	pSetForegroundWindow.Call(uintptr(w.wnd))
	cmd, _, _ := pTrackPopupMenu.Call(m, tpmReturnCmd|tpmRightButton|tpmBottomAlign, uintptr(p.X), uintptr(p.Y), 0, uintptr(w.wnd), 0)
	pPostMessageW.Call(uintptr(w.wnd), wmNull, 0, 0)
	switch cmd {
	case cmdOpen:
		if err := open(w.t.URL); err != nil {
			log.Printf("ir-computer: open: %v", err)
		}
	case cmdAtLogin:
		if err := w.t.SetAtLogin(!atLogin); err != nil {
			log.Printf("ir-computer: %s: %v", labelAtLogin, err)
		}
	case cmdQuit:
		w.t.Quit()
	}
}

func wndProc(hwnd, m, wp, lp uintptr) uintptr {
	w := cur
	switch {
	case w == nil:
	case m == wmTray && (lp&0xffff == wmLButtonUp || lp&0xffff == wmRButtonUp):
		w.menu()
		return 0
	case m == wmState:
		w.show()
		w.notify(nimModify)
		return 0
	case m == wmClose:
		pDestroyWindow.Call(hwnd)
		return 0
	case m == wmDestroy:
		w.notify(nimDelete)
		pPostQuitMessage.Call(0)
		return 0
	case m == w.taskbarCreated && m != 0:
		w.notify(nimAdd)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
	return r
}
