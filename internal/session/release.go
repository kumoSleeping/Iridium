package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/kumoSleeping/iridium/internal/agent"
)

// A release: the running process put in manual mode for a while, a person having approved its
// agent's ask for it (manual.go): only the process, the session's setting stays, so a restart is
// back in its own mode. It ends with the turn under way when it was approved (none under way: the
// next), or when a person has answered its steps (released.left), whichever comes first; then the mode goes back, unless a person
// set one meanwhile. A step auto mode denied is a failed call like any other: Claude may ask for
// this (kumo, 2026-11-25). A release is kept on disk (fileRelease): a daemon taking the process up
// goes on with it, rather than leave the process in manual mode.

// releaseMode is the mode a release runs in: every call not allowed by a rule is asked.
const releaseMode = "manual"

// fileRelease: a release under way (releaseMeta), in the hold directory.
const fileRelease = "release.json"

// releaseMeta is a release on disk: the process it put in manual mode, the mode to go back to,
// the steps it has left (0: one of before the steps, ending with its turn alone).
type releaseMeta struct {
	Proc int    `json:"proc"`
	Prev string `json:"prev"`
	Left int    `json:"left,omitempty"`
}

// released is a release under way: the mode to go back to; at: when it began; during: a turn
// was under way then, its end ends the release; left: the steps a person may still answer in it,
// the last ending it (0: no such end).
type released struct {
	prev   string
	at     time.Time
	during bool
	left   int
}

// setRelLocked starts a release, or ends it (nil), on disk too.
func (h *Hosted) setRelLocked(r *released) {
	h.rel = r
	if h.dir == "" {
		return
	}
	path := filepath.Join(h.dir, fileRelease)
	if r == nil {
		_ = os.Remove(path)
		return
	}
	if err := writeJSON(path, releaseMeta{Proc: h.proc, Prev: r.prev, Left: r.left}); err != nil {
		h.logf("release: %v", err)
	}
}

// dropRelLocked forgets the release under way, if any, leaving its file.
func (h *Hosted) dropRelLocked() { h.rel = nil }

// releaseStep: a person answered a step asked in manual mode. It is one of the release's, if one
// with steps is under way; its last, the mode goes back.
func (h *Hosted) releaseStep() {
	h.mu.Lock()
	r := h.rel
	if r == nil || r.left == 0 {
		h.mu.Unlock()
		return
	}
	if r.left--; r.left > 0 {
		h.setRelLocked(r)
		h.mu.Unlock()
		return
	}
	h.setRelLocked(nil)
	mode := h.backLocked(r)
	h.mu.Unlock()
	if mode != "" {
		h.logf("manual mode's steps answered: back to %s mode", mode)
		h.restoreMode(mode)
	}
}

// helloReleaseLocked: process proc said hello in mode. The process an earlier daemon put in manual
// mode for a release, taken up, is in it still: the release goes on (from now, the turn under way
// ending it; its steps as they were left). Else none is under way.
func (h *Hosted) helloReleaseLocked(proc int, mode string) {
	h.dropRelLocked()
	if h.dir == "" {
		return
	}
	var m releaseMeta
	b, err := os.ReadFile(filepath.Join(h.dir, fileRelease))
	if err != nil || json.Unmarshal(b, &m) != nil || m.Proc != proc || !isManual(mode) || isManual(m.Prev) {
		h.setRelLocked(nil)
		return
	}
	busy := h.turn != "" && h.turn != agent.Idle
	h.setRelLocked(&released{prev: m.Prev, at: time.Now(), during: busy, left: m.Left})
	h.mode = releaseMode
	h.logf("manual mode goes on (back to %s mode after)", m.Prev)
}

// isManual: Claude's mode that asks for every call not allowed by a rule ("default" its old name).
func isManual(mode string) bool { return mode == releaseMode || mode == "default" }

// releaseOverLocked ends a release with the turn under way at it, or begun after it. It gives the
// mode to go back to, "" for none.
func (h *Hosted) releaseOverLocked() string {
	r := h.rel
	if r == nil || (!r.during && !h.turnAt.After(r.at)) {
		return ""
	}
	h.setRelLocked(nil)
	return h.backLocked(r)
}

// backLocked is the mode to go back to after release r, "" for none: you set one meanwhile, or
// it was asking already.
func (h *Hosted) backLocked(r *released) string {
	if h.mode != releaseMode || isManual(r.prev) {
		return ""
	}
	return r.prev
}

// restoreMode puts the process back in mode after a release.
func (h *Hosted) restoreMode(mode string) {
	ctx, cancel := context.WithTimeout(h.m.ctx, 30*time.Second)
	defer cancel()
	if !h.isLive() {
		return
	}
	if err := h.drv.SetMode(ctx, mode); err != nil {
		h.logf("release: back to %s mode: %v", mode, err)
		return
	}
	h.mu.Lock()
	if h.rel == nil {
		h.mode = mode
	}
	h.mu.Unlock()
	h.publish()
	h.m.publishList()
}
