package webapp

import (
	"testing"
	"time"
)

func TestFlows(t *testing.T) {
	f := newFlows()
	now := time.Unix(1e9, 0)
	id := f.put(&flow{kind: flowLogin}, now)
	if f.take(id, flowJoin, now) != nil {
		t.Fatal("took a flow of the wrong kind")
	}
	id = f.put(&flow{kind: flowLogin}, now)
	if f.take(id, flowLogin, now) == nil || f.take(id, flowLogin, now) != nil {
		t.Fatal("flow not single use")
	}
	id = f.put(&flow{kind: flowLogin}, now)
	if f.take(id, flowLogin, now.Add(flowTTL)) != nil {
		t.Fatal("expired flow taken")
	}
	for i := 0; i < maxFlows+10; i++ {
		f.put(&flow{kind: flowLogin}, now.Add(time.Duration(i)))
	}
	if len(f.m) > maxFlows {
		t.Fatalf("%d flows", len(f.m))
	}
}
