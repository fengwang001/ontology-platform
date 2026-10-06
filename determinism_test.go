package store

import (
	"reflect"
	"testing"
)

func TestSameSequenceAndCrashPointReplaysIdentically(t *testing.T) {
	run := func() (PersistedState, PersistedState) {
		s := New()
		s.SetCrashHookForTest(func(lsn int64) bool { return lsn == 5 })
		_, _ = s.Put(Row{Primary: "p1", Secondary: strPtr("a")})
		_, _ = s.Put(Row{Primary: "p2", Secondary: strPtr("b")})
		_, _ = s.Put(Row{Primary: "p1", Secondary: nil})
		_, _ = s.Put(Row{Primary: "p3", Secondary: strPtr("a")})
		_, _ = s.Put(Row{Primary: "p2", Secondary: strPtr("c")})
		_, _ = s.Delete("p3")
		_, _ = s.Put(Row{Primary: "p4", Secondary: strPtr("b")})
		_, _ = s.CatchUp(3)
		crashed := s.PersistedSnapshot()
		resumed := New()
		if err := resumed.Restart(crashed); err != nil {
			t.Fatalf("restart: %v", err)
		}
		for resumed.Watermark() < resumed.LastLSN() {
			if _, err := resumed.CatchUp(2); err != nil {
				t.Fatalf("resume catch-up: %v", err)
			}
		}
		return crashed, resumed.PersistedSnapshot()
	}
	crashedA, finalA := run()
	crashedB, finalB := run()
	if !reflect.DeepEqual(crashedA, crashedB) {
		t.Fatalf("crash states differ:\n%#v\n%#v", crashedA, crashedB)
	}
	if !reflect.DeepEqual(finalA, finalB) {
		t.Fatalf("final states differ:\n%#v\n%#v", finalA, finalB)
	}
}
