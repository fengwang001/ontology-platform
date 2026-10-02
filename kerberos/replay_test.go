package kerberos

import "testing"

func TestReplayCleanupPopCount(t *testing.T) {
	cache := newReplayCache()
	cache.add(replayKey{ticket: "t1", authTime: 1}, 2)
	cache.add(replayKey{ticket: "t2", authTime: 2}, 3)
	cache.add(replayKey{ticket: "t3", authTime: 95}, 100)

	cache.cleanup(4)
	if cache.pops != 2 || len(cache.entries) != 1 {
		t.Fatalf("first cleanup pops=%d size=%d", cache.pops, len(cache.entries))
	}
	before := cache.pops
	cache.cleanup(100)
	if cache.pops-before != 0 || len(cache.entries) != 1 {
		t.Fatalf("boundary cleanup delta=%d size=%d", cache.pops-before, len(cache.entries))
	}
	cache.cleanup(101)
	if cache.pops-before != 1 || len(cache.entries) != 0 {
		t.Fatalf("after expiry delta=%d size=%d", cache.pops-before, len(cache.entries))
	}
}
