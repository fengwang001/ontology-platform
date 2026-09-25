package mbatch

import "testing"

// Close determination must locate windows by end order (heap), not scan
// accumulated history. Watermark semantics keep at most one window open
// after a trigger, so history accumulates as closed windows; the heap
// drops them, keeping reads within (closed this trigger)+constant.
func TestCloseScanReadsIndependentOfHistory(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		e := New(1, 1)
		for i := int64(0); i < m; i++ {
			e.Feed([]Event{{Key: "k", TS: i}})
		}
		e.Feed([]Event{{Key: "k", TS: m}}) // wm +1: closes exactly 1 window
		if e.reads > 1+4 {
			t.Errorf("m=%d: reads exceed closed(1)+const", m)
		}
		e.Feed([]Event{{Key: "k", TS: m}}) // closes 0 windows
		if e.reads > 4 {
			t.Errorf("m=%d: idle reads not constant", m)
		}
	}
}
