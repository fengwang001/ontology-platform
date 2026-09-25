package snapshot

import (
	"errors"
	"fmt"
	"testing"

	"ontology/store"
)

// TestConsistency 用同一张表覆盖：导出到指定键时另一键被改写，
// 快照始终读到旧值；以及空键、空值与“不存在”的区分。
func TestConsistency(t *testing.T) {
	cases := []struct {
		name     string
		n        int
		mutateAt int
		target   string
	}{
		{"mutate key80 while at key50", 100, 50, fmt.Sprintf("k%03d", 80)},
		{"mutate at first position", 10, 0, fmt.Sprintf("k%03d", 5)},
		{"mutate at last position", 10, 9, fmt.Sprintf("k%03d", 0)},
		{"empty key and empty value", 3, 1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.New()
			for i := 0; i < tc.n; i++ {
				st.Put(fmt.Sprintf("k%03d", i), []byte(fmt.Sprintf("v%d", i)))
			}
			st.Put("", []byte{})
			snap := New(st)
			keys := st.Keys(snap.Version(), "asc")
			for i, k := range keys {
				if i == tc.mutateAt {
					st.Put(tc.target, []byte("NEW"))
				}
				val, ok, err := snap.Read(k)
				if err != nil {
					t.Fatalf("read %q: %v", k, err)
				}
				if !ok {
					t.Fatalf("key %q should exist in snapshot", k)
				}
				if k == tc.target && string(val) != oldValue(tc.target) {
					t.Fatalf("key %q = %q, want snapshot-old value", k, val)
				}
				if k == "" && string(val) != "" {
					t.Fatalf("empty key value = %q, want empty", val)
				}
			}
			if _, ok, err := snap.Read("missing"); err != nil || ok {
				t.Fatalf("missing key: ok=%v err=%v", ok, err)
			}
			snap.Close()
		})
	}
}

func oldValue(key string) string {
	if key == "" {
		return ""
	}
	var idx int
	fmt.Sscanf(key, "k%03d", &idx)
	return fmt.Sprintf("v%d", idx)
}

// TestRetention 覆盖保留计数：0/部分/全部改写、两个重叠快照、
// 10 万键中只改 100 个、关闭后归零。
func TestRetention(t *testing.T) {
	cases := []struct {
		name    string
		total   int
		mutated int
	}{
		{"mutate 0 of small", 50, 0},
		{"mutate 100 of 100000", 100000, 100},
		{"mutate all of small", 16, 16},
		{"single key mutated", 1, 1},
		{"empty store", 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.New()
			for i := 0; i < tc.total; i++ {
				st.Put(fmt.Sprintf("k%06d", i), []byte("v"))
			}
			s1 := New(st)
			s2 := New(st)
			for i := 0; i < tc.mutated; i++ {
				st.Put(fmt.Sprintf("k%06d", i), []byte("w"))
			}
			if got := st.Retained(); got != tc.mutated {
				t.Fatalf("retained=%d want %d", got, tc.mutated)
			}
			s2.Close()
			if got := st.Retained(); got != tc.mutated {
				t.Fatalf("after s2 close retained=%d want %d", got, tc.mutated)
			}
			s1.Close()
			if got := st.Retained(); got != 0 {
				t.Fatalf("after both close retained=%d want 0", got)
			}
		})
	}
}

// TestClosedReads 关闭后读必须返回 ErrClosed。
func TestClosedReads(t *testing.T) {
	st := store.New()
	st.Put("k", []byte("v"))
	snap := New(st)
	snap.Close()
	if _, _, err := snap.Read("k"); !errors.Is(err, ErrClosed) {
		t.Fatalf("err=%v want ErrClosed", err)
	}
	if _, err := snap.Keys("asc"); !errors.Is(err, ErrClosed) {
		t.Fatalf("keys err=%v want ErrClosed", err)
	}
}
