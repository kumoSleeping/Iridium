package discover

import (
	"time"

	"github.com/kumoSleeping/iridium/internal/api"
)

// Activity on disk sets a session's shown marker (store/shown.go, the user's decision of
// 2026-11-05): a transcript never seen whose last change is after Iridium began watching, or one
// that grew past the size noted when it was hidden and was written after that. What was there
// before and has not changed since stays hidden: that is the archive.

// hiddenSlack: a file's time is the kernel's coarse clock, a few ms behind; a transcript written
// less than this before it was hidden may be growth after.
const hiddenSlack = time.Second

// since is when Iridium began watching (ms), asked of the store the first time.
func (x *Index) watchingSince() int64 {
	x.sinceOnce.Do(func() {
		if x.o.Store == nil {
			return
		}
		at, err := x.o.Store.WatchingSince()
		if err != nil {
			x.o.Logf("discover: %v", err)
			at = time.Now().UnixMilli()
		}
		x.since = at
	})
	return x.since
}

// activity: agent's transcript of session id was looked at again, and is size long, changed
// last at mtime; grew: longer than at the last look of this daemon (a file's time can lag the
// clock by a few ms, so growth seen counts whatever its time). True if that showed the session.
func (x *Index) activity(agent api.Agent, id string, size int64, mtime time.Time, grew bool) bool {
	st := x.o.Store
	if st == nil {
		return false
	}
	since := x.watchingSince()
	r, ok, err := st.ShownRow(string(agent), id)
	if err != nil {
		x.o.Logf("discover: %v", err)
		return false
	}
	switch {
	case !ok:
		if !grew && mtime.UnixMilli() <= since {
			return false // there before Iridium watched, and not touched since
		}
	case r.Shown:
		return false
	case r.Legacy:
		// Hidden by the old archive flag: from now on by its agent, from this size.
		if err := st.Hide(string(agent), id, size, 0); err != nil {
			x.o.Logf("discover: %v", err)
		}
		return false
	case size <= r.Size || time.Now().UnixMilli() < r.QuietUntil:
		return false
	case mtime.UnixMilli() < r.At-hiddenSlack.Milliseconds():
		// Not written since it was archived: longer, but not by growth (another copy of it, or
		// written between the last look and the archiving).
		return false
	}
	changed, err := st.Show(string(agent), id)
	if err != nil {
		x.o.Logf("discover: %v", err)
		return false
	}
	return changed
}

// Size is how long session id's transcript was when last looked at; false if not on disk.
func (x *Index) Size(id string) (int64, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if e := x.entries[id]; e != nil {
		return e.size, true
	}
	return 0, false
}
