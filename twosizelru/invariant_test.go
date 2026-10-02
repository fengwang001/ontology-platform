package twosizelru_test

import (
	"testing"

	"ontology/twosizelru"
)

func TestReadCountEqualsEvictionsPlusResidentCount(t *testing.T) {
	manager := newManager(t, 4, 50, 0, 100)
	reads := 0
	evictions := 0

	for i, now := 0, int64(0); i < 12; i, now = i+1, now+1 {
		var result twosizelru.Result
		if i%3 == 0 {
			result = mustPrefetch(t, manager, i%7, now)
		} else {
			result = mustAccess(t, manager, i%7, now)
		}
		if result.Read {
			reads++
		}
		if result.Evicted {
			evictions++
		}

		young, old := manager.Lists()
		if got := reads; got != evictions+len(young)+len(old) {
			t.Fatalf("reads=%d evictions=%d residents=%d", got, evictions, len(young)+len(old))
		}
	}
}
