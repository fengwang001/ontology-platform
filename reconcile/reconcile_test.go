package reconcile

import (
	"fmt"
	"reflect"
	"testing"

	"ontology/batch"
	"ontology/store"
)

// fixture 构造清单与存储：present 中的下标写入存储，extra 为多余记录。
func fixture(n int, present map[int]bool, extra ...string) (batch.Manifest, *store.Store) {
	m := batch.Manifest{ID: "b"}
	st := store.New()
	for i := 0; i < n; i++ {
		m.Keys = append(m.Keys, fmt.Sprintf("k%06d", i))
		if present[i] {
			st.Write(m.Keys[i], []byte(m.Keys[i]))
		}
	}
	for _, k := range extra {
		st.Write(k, []byte(k))
	}
	return m, st
}

func idxs(ranges ...int) map[int]bool {
	m := map[int]bool{}
	for i := 0; i < len(ranges); i += 2 {
		for j := ranges[i]; j < ranges[i+1]; j++ {
			m[j] = true
		}
	}
	return m
}

func TestReconcile(t *testing.T) {
	cases := []struct {
		name                       string
		n                          int
		present                    map[int]bool
		extra                      []string
		committed                  bool
		wantWritten, wantGaps      [][2]int
		wantCommitted, wantTransit int
	}{
		{"three segments", 10, idxs(0, 5, 7, 9), []string{"zzz"}, false,
			[][2]int{{0, 5}, {7, 9}}, [][2]int{{5, 7}, {9, 10}}, 0, 7},
		{"committed full", 5, idxs(0, 5), nil, true,
			[][2]int{{0, 5}}, nil, 5, 0},
		{"in-transit not committed", 6, idxs(0, 3), nil, false,
			[][2]int{{0, 3}}, [][2]int{{3, 6}}, 0, 3},
		{"empty store", 3, idxs(), nil, false,
			nil, [][2]int{{0, 3}}, 0, 0},
		{"progress-store mismatch gap", 100000, idxs(0, 30000), nil, false,
			[][2]int{{0, 30000}}, [][2]int{{30000, 100000}}, 0, 30000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, st := fixture(tc.n, tc.present, tc.extra...)
			r := Reconcile(m, st, tc.committed)
			if !reflect.DeepEqual(r.Written, tc.wantWritten) {
				t.Fatalf("written=%v want %v", r.Written, tc.wantWritten)
			}
			if !reflect.DeepEqual(r.Gaps, tc.wantGaps) {
				t.Fatalf("gaps=%v want %v", r.Gaps, tc.wantGaps)
			}
			if !reflect.DeepEqual(r.Extra, tc.extra) {
				t.Fatalf("extra=%v want %v", r.Extra, tc.extra)
			}
			if r.Committed != tc.wantCommitted || r.InTransit != tc.wantTransit {
				t.Fatalf("committed=%d in-transit=%d", r.Committed, r.InTransit)
			}
			if got := st.Reads(); got > 3*tc.n {
				t.Fatalf("store accesses=%d exceeds 3n=%d", got, 3*tc.n)
			}
		})
	}
}
