package replay

import (
	"testing"

	"ontology/es"
)

// TestLocateSublinear 钉住复杂度：定位比较次数不随 m 线性增长（O(log m)）。
// 白盒测试：与 replay 同包，直接读非导出字段 locateCmp，不经公开接口。
func TestLocateSublinear(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		evs := make([]es.Event, m)
		for i := range evs {
			evs[i] = es.Event{Seq: int64(i + 1), Delta: 1}
		}
		r := New()
		snap := es.Snapshot{Seq: int64(m / 2), Total: int64(m / 2)}
		got, err := r.Replay(snap, evs)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if want := int64(m); got != want {
			t.Fatalf("m=%d: balance=%d want %d", m, got, want)
		}
		// 线性扫描需 >= m/2 次比较；二分上界约为 log2(m)+1，留足余量仍远小于 m/2。
		if limit := 8*log2ceil(m) + 8; r.locateCmp > limit {
			t.Fatalf("m=%d: locateCmp=%d 超过 O(log m) 上界 %d", m, r.locateCmp, limit)
		}
		if m >= 100 && r.locateCmp >= m/2 {
			t.Fatalf("m=%d: locateCmp=%d 随 m 线性增长", m, r.locateCmp)
		}
	}
}

func log2ceil(n int) int {
	k := 0
	for x := 1; x < n; x <<= 1 {
		k++
	}
	return k
}

// TestReplayRejects 表驱动：重放非法（乱序 / Seq<=0）与快照非法可判定且互不相同。
func TestReplayRejects(t *testing.T) {
	cases := []struct {
		name string
		snap es.Snapshot
		evs  []es.Event
		want error
	}{
		{"快照Seq为负", es.Snapshot{Seq: -1}, nil, es.ErrBadSnapshot},
		{"快照Total为负", es.Snapshot{Total: -1}, nil, es.ErrBadSnapshot},
		{"列表Seq递减", es.Snapshot{}, []es.Event{{Seq: 2, Delta: 1}, {Seq: 1, Delta: 1}}, ErrBadReplay},
		{"列表含Seq0", es.Snapshot{}, []es.Event{{Seq: 0, Delta: 1}}, ErrBadReplay},
		{"列表含负Seq", es.Snapshot{}, []es.Event{{Seq: -3, Delta: 1}}, ErrBadReplay},
	}
	for _, c := range cases {
		if _, err := New().Replay(c.snap, c.evs); err != c.want {
			t.Errorf("%s: err=%v want %v", c.name, err, c.want)
		}
	}
}
