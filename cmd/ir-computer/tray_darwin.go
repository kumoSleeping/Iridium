package main

import (
	"context"
	"errors"
	"log"
	"os"
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"

	"github.com/kumoSleeping/iridium/internal/join"
)

// The tray on macOS: an item in the menu bar (NSStatusItem), through the Objective-C runtime
// (purego: no cgo). AppKit wants the main thread: main's goroutine stays on it, and the tray runs
// there (join.Program.Tray).

func init() { runtime.LockOSThread() }

var sel = objc.RegisterName

func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

func nsString(s string) objc.ID { return class("NSString").Send(sel("stringWithUTF8String:"), s) }

type macTray struct {
	t       join.Tray
	button  objc.ID // the item's, with the mark
	status  objc.ID // the menu's first line
	atLogin objc.ID
	shown   join.State
	fresh   bool
}

// cur is the one tray: its menu's target's.
var cur *macTray

func tray(ctx context.Context, t join.Tray) error {
	if os.Getenv("SSH_CONNECTION") != "" {
		return errors.New("over ssh")
	}
	if _, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
		return err
	}
	pool := class("NSAutoreleasePool").Send(sel("new"))
	defer pool.Send(sel("drain"))
	app := class("NSApplication").Send(sel("sharedApplication"))
	app.Send(sel("setActivationPolicy:"), 1) // accessory: no Dock icon, no menu of its own

	m := &macTray{t: t}
	cur = m
	target, err := objc.RegisterClass("IRComputerTarget", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
		{Cmd: sel("open:"), Fn: func(objc.ID, objc.SEL, objc.ID) {
			if err := open(cur.t.URL); err != nil {
				log.Printf("ir-computer: open: %v", err)
			}
		}},
		{Cmd: sel("atLogin:"), Fn: func(objc.ID, objc.SEL, objc.ID) {
			if err := cur.t.SetAtLogin(!cur.t.AtLogin()); err != nil {
				log.Printf("ir-computer: %s: %v", labelAtLogin, err)
			}
		}},
		{Cmd: sel("quit:"), Fn: func(objc.ID, objc.SEL, objc.ID) { cur.t.Quit() }},
		// NSMenuDelegate: the switch as it is when the menu opens.
		{Cmd: sel("menuNeedsUpdate:"), Fn: func(objc.ID, objc.SEL, objc.ID) {
			state := 0
			if cur.t.AtLogin() {
				state = 1
			}
			cur.atLogin.Send(sel("setState:"), state)
		}},
	})
	if err != nil {
		return err
	}
	to := objc.ID(target).Send(sel("new"))

	bar := class("NSStatusBar").Send(sel("systemStatusBar"))
	item := bar.Send(sel("statusItemWithLength:"), -1.0) // NSVariableStatusItemLength
	if item == 0 {
		return errors.New("no status item")
	}
	item.Send(sel("retain"))
	defer bar.Send(sel("removeStatusItem:"), item)
	m.button = item.Send(sel("button"))
	b := pngOf(mark(36, true, false), 144) // 18 points, @2x
	data := class("NSData").Send(sel("dataWithBytes:length:"), unsafe.Pointer(&b[0]), len(b))
	img := class("NSImage").Send(sel("alloc")).Send(sel("initWithData:"), data)
	img.Send(sel("setTemplate:"), true)
	m.button.Send(sel("setImage:"), img)

	menu := class("NSMenu").Send(sel("alloc")).Send(sel("initWithTitle:"), nsString(""))
	menu.Send(sel("setAutoenablesItems:"), false)
	menu.Send(sel("setDelegate:"), to)
	add := func(title, action string) objc.ID {
		var a objc.SEL
		if action != "" {
			a = sel(action)
		}
		it := class("NSMenuItem").Send(sel("alloc")).Send(sel("initWithTitle:action:keyEquivalent:"), nsString(title), a, nsString(""))
		if action != "" {
			it.Send(sel("setTarget:"), to)
		} else {
			it.Send(sel("setEnabled:"), false)
		}
		menu.Send(sel("addItem:"), it)
		return it
	}
	m.status = add(line(t.URL, t.Status.Get()), "")
	add(labelOpen, "open:")
	m.atLogin = add(labelAtLogin, "atLogin:")
	menu.Send(sel("addItem:"), class("NSMenuItem").Send(sel("separatorItem")))
	add(labelQuit, "quit:")
	item.Send(sel("setMenu:"), menu)
	app.Send(sel("finishLaunching"))

	// The run loop, by hand: it looks at ctx and the state between events, 4 times a second at
	// least.
	mode := nsString("kCFRunLoopDefaultMode").Send(sel("retain"))
	const anyEvent = ^uint64(0)
	for ctx.Err() == nil {
		p := class("NSAutoreleasePool").Send(sel("new"))
		until := class("NSDate").Send(sel("dateWithTimeIntervalSinceNow:"), 0.25)
		if ev := app.Send(sel("nextEventMatchingMask:untilDate:inMode:dequeue:"), anyEvent, until, mode, true); ev != 0 {
			app.Send(sel("sendEvent:"), ev)
		}
		m.show()
		p.Send(sel("drain"))
	}
	return nil
}

// show puts the state in the menu's first line and the mark's tip; not connected, the mark faded.
func (m *macTray) show() {
	st := m.t.Status.Get()
	if m.fresh && st == m.shown {
		return
	}
	m.shown, m.fresh = st, true
	l := line(m.t.URL, st)
	m.status.Send(sel("setTitle:"), nsString(l))
	m.button.Send(sel("setToolTip:"), nsString(l))
	m.button.Send(sel("setAppearsDisabled:"), !st.Online)
}
