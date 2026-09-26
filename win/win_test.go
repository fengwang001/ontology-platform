package win

import (
	"reflect"
	"testing"
)

func Test_EvictExpiredQueue(t *testing.T) {
	cases := []struct {
		name    string
		seed    []int64
		now     int64
		window  int64
		want    []int64
		wantCnt int // InWindow(now) after eviction
	}{
		{"empty", nil, 5, 10, nil, 0},
		{"none expired", []int64{0, 2, 5}, 10, 10, []int64{0, 2, 5}, 3},
		{"left edge kept", []int64{2, 5, 11}, 12, 10, []int64{2, 5, 11}, 3},
		{"oldest dropped", []int64{0, 2, 5}, 11, 10, []int64{2, 5}, 2},
		{"all expired", []int64{0, 1, 2}, 20, 10, []int64{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := &Queue{}
			for _, ts := range c.seed {
				q.Push(ts)
			}
			q.EvictExpired(c.now, c.window)
			got := q.Snapshot()
			if len(got) != len(c.want) || (len(got) > 0 && !reflect.DeepEqual(got, c.want)) {
				t.Fatalf("after eviction = %v, want %v", got, c.want)
			}
			if got := q.InWindow(c.now); got != c.wantCnt {
				t.Fatalf("InWindow = %d, want %d", got, c.wantCnt)
			}
		})
	}
}

func Test_EvictionProbesBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		q := &Queue{}
		for i := 0; i < m; i++ {
			q.Push(int64(i)) // dense adjacent timestamps, all still in window
		}
		q.EvictExpired(int64(m), int64(m)+1) // oldest 0 >= m-(m+1) = -1: alive
		if q.Len() != m {
			t.Fatalf("m=%d: Len = %d, want %d (nothing should expire)", m, q.Len(), m)
		}
		if q.probes > 1 {
			t.Fatalf("m=%d: probes = %d, want <= 1 (head peek, no full scan)", m, q.probes)
		}
	}
}
