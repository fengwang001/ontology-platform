package tablespace

import "testing"

func TestDbgQ(t *testing.T) {
	a, _ := New(3, 2, 6)
	s := a.NewSegment()
	for _, p := range []int{0, 1, 3, 4, 5, 6} {
		mustAlloc(t, a, s, -1, p)
	}
	a.FreePage(s, 3)
	seg := a.segments[s]
	t.Logf("qHead=%d hint=-1, used(seg)=%d", seg.qHead, seg.used)
	eid := seg.qHead
	t.Logf("firstFreeOffset(%d)=%d", eid, a.firstFreeOffset(eid))
}
