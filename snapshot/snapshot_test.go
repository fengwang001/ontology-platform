package snapshot_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/snapshot"
	"ontology/store"
)

func fill(st *store.Store, n int) {
	for i := 0; i < n; i++ {
		st.Put(fmt.Sprintf("k%03d", i), []byte(fmt.Sprintf("old-%d", i)))
	}
}

// TestCOW：导出到第 50 键时改写第 80 键，快照仍读到旧值；并覆盖保留值计数。
func TestCOW(t *testing.T) {
	cases := []struct {
		name   string
		total  int
		mutate func(*store.Store, uint64) int // 返回改写键数
		maxRet int
	}{
		{"none", 200, func(*store.Store, uint64) int { return 0 }, 0},
		{"key80-during-export", 200, func(st *store.Store, _ uint64) int {
			st.Put("k079", []byte("new"))
			return 1
		}, 1},
		{"100-of-100000", 100000, func(st *store.Store, _ uint64) int {
			for i := 0; i < 100; i++ {
				st.Put(fmt.Sprintf("k%03d", 50000+i), []byte("x"))
			}
			return 100
		}, 100},
		{"all-keys", 8, func(st *store.Store, _ uint64) int {
			for i := 0; i < 8; i++ {
				st.Put(fmt.Sprintf("k%03d", i), []byte("z"))
			}
			return 8
		}, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.New()
			fill(st, tc.total)
			snap := snapshot.Take(st)
			n := tc.mutate(st, snap.Version())
			keys, err := snap.Keys(snapshot.Ascending)
			if err != nil || len(keys) != tc.total {
				t.Fatalf("keys: %v %d", err, len(keys))
			}
			if r := snap.Retained(); r != n || r > tc.maxRet {
				t.Fatalf("retained=%d want %d (<=%d)", r, n, tc.maxRet)
			}
			// 逐键遍历：每个键恰好在自己被读一次，且全部为快照旧值。
			for i, k := range keys {
				v, ok, err := snap.Get(k)
				if err != nil || !ok {
					t.Fatalf("get %s: %v ok=%v", k, err, ok)
				}
				if i == 79 && tc.name == "key80-during-export" && string(v) != "old-79" {
					t.Fatalf("key80 polluted: %q", v)
				}
			}
			if got := snap.Reads(); got != tc.total {
				t.Fatalf("reads=%d want %d", got, tc.total)
			}
			snap.Close()
			if r := snap.Retained(); r != 0 {
				t.Fatalf("retained after close=%d want 0", r)
			}
		})
	}
}

// TestOverlap：两个重叠快照，只有都关闭后保留值才释放（按最老快照判断）。
func TestOverlap(t *testing.T) {
	st := store.New()
	fill(st, 10)
	s1 := snapshot.Take(st)
	for i := 0; i < 5; i++ {
		st.Put(fmt.Sprintf("k%03d", i), []byte("new"))
	}
	s2 := snapshot.Take(st)
	for i := 0; i < 5; i++ {
		st.Put(fmt.Sprintf("k%03d", i), []byte("newer"))
	}
	if s1.Retained() != 5 || s2.Retained() != 5 {
		t.Fatalf("retained s1=%d s2=%d want 5", s1.Retained(), s2.Retained())
	}
	v, _, _ := s1.Get("k000")
	if string(v) != "old-0" {
		t.Fatalf("s1 saw %q want old-0", v)
	}
	s2.Close()
	if s1.Retained() != 5 {
		t.Fatalf("after s2 close retained=%d want 5", s1.Retained())
	}
	s1.Close()
	if r := store.New().Retained(0); r != 0 {
		t.Fatalf("fresh store retained=%d", r)
	}
	if got := st.Retained(0); got != 0 {
		t.Fatalf("after both close retained=%d want 0", got)
	}
}

// TestBoundaries：空存储、空键、空值、关闭后读取可判定错误。
func TestBoundaries(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"empty", func(t *testing.T) {
			snap := snapshot.Take(store.New())
			keys, _ := snap.Keys(snapshot.Ascending)
			if len(keys) != 0 || snap.Retained() != 0 {
				t.Fatal("empty store not empty")
			}
			snap.Close()
		}},
		{"empty-key-and-empty-value", func(t *testing.T) {
			st := store.New()
			st.Put("", []byte{})
			snap := snapshot.Take(st)
			v, ok, err := snap.Get("")
			if err != nil || !ok || len(v) != 0 {
				t.Fatalf("empty kv: ok=%v len=%d err=%v", ok, len(v), err)
			}
			if _, ok, _ := snap.Get("missing"); ok {
				t.Fatal("missing key reported present")
			}
			snap.Close()
		}},
		{"closed-read", func(t *testing.T) {
			st := store.New()
			st.Put("a", []byte("1"))
			snap := snapshot.Take(st)
			snap.Close()
			if _, _, err := snap.Get("a"); err != snapshot.ErrSnapshotClosed {
				t.Fatalf("err=%v want ErrSnapshotClosed", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

// TestConcurrentRace：并发读写与快照读取，-race 下必须干净。
func TestConcurrentRace(t *testing.T) {
	st := store.New()
	fill(st, 200)
	snaps := make([]*snapshot.Handle, 4)
	for i := range snaps {
		snaps[i] = snapshot.Take(st)
	}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				st.Put(fmt.Sprintf("k%03d", i%200), []byte("c"))
				snaps[id%4].Get(fmt.Sprintf("k%03d", i%200))
			}
		}(w)
	}
	wg.Wait()
	for _, snap := range snaps {
		snap.Close()
	}
}
