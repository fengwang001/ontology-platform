package kvlog

import (
	"os"
	"testing"
)

// rebuildModelFromDisk is a fully independent logical reconstruction: it
// scans every currently-present segment in ascending id order and keeps
// the record with the globally largest seq per key. It deliberately does
// not consult the engine keydir or hints.
func rebuildModelFromDisk(t *testing.T, e *Engine, dir string) *naiveModel {
	t.Helper()
	states, err := listSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	type cell struct {
		seq  uint64
		tomb bool
		val  string
	}
	best := map[string]cell{}
	for _, st := range states {
		data, rerr := os.ReadFile(dir + "/" + segLogName(st.id))
		if rerr != nil {
			t.Fatal(rerr)
		}
		res := scanRecords(data, !st.sealed)
		if res.outcome == scanCorrupt {
			t.Fatalf("model scan corrupt seg=%d off=%d", st.id, res.badOffset)
		}
		for _, f := range res.frames {
			k := string(f.rec.key)
			c, ok := best[k]
			if !ok || f.rec.seq > c.seq {
				best[k] = cell{
					seq:  f.rec.seq,
					tomb: f.rec.tomb,
					val:  string(f.rec.value),
				}
			}
		}
	}
	m := newNaiveModel()
	for k, c := range best {
		if c.tomb {
			m.del(k)
		} else {
			m.put(k, c.val)
		}
	}
	return m
}
