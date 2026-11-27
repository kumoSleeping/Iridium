package server

import (
	"sync"
	"sync/atomic"
	"testing"
)

// What is added for an agent as its last one is done is done once, by one goroutine: run let go
// of an emptied queue before it left, an add then started a second run, both did the same one and
// the second sliced an empty queue (a panic on CI).
func TestAnswersOneRun(t *testing.T) {
	var a answers
	var done atomic.Int64
	const n = 2000
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.add("s"+string(rune('a'+i%3)), func() { done.Add(1) })
		}()
	}
	wg.Wait()
	a.wg.Wait()
	if got := done.Load(); got != n {
		t.Fatalf("done %d times, want %d", got, n)
	}
}
