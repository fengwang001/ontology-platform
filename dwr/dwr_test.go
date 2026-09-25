package dwr

import (
	"fmt"
	"maps"
	"testing"

	"ontology/rec"
)

// w 表示一条写：op 0=Put 1=Del 2=PutOne 3=DelOne。
type w struct {
	op, side int
	k, v     string
	ver      int64
}

func apply(s *Store, ws []w) {
	for _, x := range ws {
		var err error
		switch x.op {
		case 0:
			err = s.Put(x.k, x.v, x.ver)
		case 1:
			err = s.Del(x.k, x.ver)
		case 2:
			err = s.PutOne(x.side, x.k, x.v, x.ver)
		case 3:
			err = s.DelOne(x.side, x.k, x.ver)
		}
		if err != nil {
			panic(err)
		}
	}
}

var seqs = map[string][]w{
	"seven": {
		{op: 0, k: "a", v: "a1", ver: 1}, {op: 0, k: "b", v: "b1", ver: 2},
		{op: 0, k: "c", v: "c1", ver: 3}, {op: 2, side: 0, k: "a", v: "a2", ver: 4},
		{op: 3, side: 1, k: "c", ver: 5}, {op: 3, side: 0, k: "b", ver: 6},
		{op: 2, side: 0, k: "d", v: "d1", ver: 7},
	},
	"tombstone-wins": {
		{op: 0, k: "x", v: "x1", ver: 1}, {op: 3, side: 1, k: "x", ver: 2},
	},
	"absent-vs-live": {
		{op: 2, side: 1, k: "only-b", v: "v", ver: 1},
	},
	"rewrite-after-diverge": {
		{op: 2, side: 0, k: "k", v: "v1", ver: 1}, {op: 0, k: "k", v: "v2", ver: 2},
	},
}

func TestConverged(t *testing.T) {
	want := map[string]map[string]string{
		"seven":                 {"a": "a2", "d": "d1"},
		"tombstone-wins":        {},
		"absent-vs-live":        {"only-b": "v"},
		"rewrite-after-diverge": {"k": "v2"},
	}
	for name, ws := range seqs {
		t.Run(name, func(t *testing.T) {
			s := New()
			apply(s, ws)
			s.Reconcile()
			if !maps.Equal(s.a, s.b) {
				t.Fatalf("A != B after reconcile: %v vs %v", s.a, s.b)
			}
			if got := s.View(); !maps.Equal(got, want[name]) {
				t.Fatalf("view=%v want %v", got, want[name])
			}
		})
	}
}

func TestIdempotent(t *testing.T) {
	for name, ws := range seqs {
		t.Run(name, func(t *testing.T) {
			s := New()
			apply(s, ws)
			s.Reconcile()
			a0, b0 := s.Replicas()
			s.Reconcile()
			a1, b1 := s.Replicas()
			if !maps.Equal(a0, a1) || !maps.Equal(b0, b1) {
				t.Fatal("second reconcile changed state")
			}
			if s.checked != 0 {
				t.Fatalf("second reconcile checked %d keys, want 0", s.checked)
			}
		})
	}
}

func TestRejectNoOp(t *testing.T) {
	cases := []struct {
		name string
		op   func(s *Store) error
		want error
	}{
		{"empty-key", func(s *Store) error { return s.Put("", "v", 2) }, rec.ErrKey},
		{"empty-key-del", func(s *Store) error { return s.DelOne(0, "", 2) }, rec.ErrKey},
		{"ver-zero", func(s *Store) error { return s.Put("k", "v", 0) }, rec.ErrVer},
		{"ver-not-increasing", func(s *Store) error { return s.Del("k", 1) }, rec.ErrVer},
		{"empty-val", func(s *Store) error { return s.PutOne(1, "k", "", 2) }, rec.ErrVal},
		{"bad-side", func(s *Store) error { return s.PutOne(2, "k", "v", 2) }, ErrSide},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New()
			if err := s.Put("k", "v1", 1); err != nil {
				t.Fatal(err)
			}
			a0, b0 := s.Replicas()
			if err := c.op(s); err != c.want {
				t.Fatalf("err=%v want %v", err, c.want)
			}
			a1, b1 := s.Replicas()
			if !maps.Equal(a0, a1) || !maps.Equal(b0, b1) {
				t.Fatal("rejected write changed replicas")
			}
			if err := s.Put("k2", "v2", 2); err != nil { // maxVer 未被推进
				t.Fatalf("maxVer moved by rejected write: %v", err)
			}
		})
	}
}

func TestCheckedScaling(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprint(m), func(t *testing.T) {
			s := New()
			for i := 0; i < m; i++ { // m 个两侧一致的键
				if err := s.Put(fmt.Sprintf("k%d", i), "v", int64(i+1)); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.PutOne(0, "divergent", "v", int64(m+1)); err != nil { // 仅 1 个分歧键
				t.Fatal(err)
			}
			s.Reconcile()
			if s.checked > 4 {
				t.Fatalf("checked=%d grows with m=%d, want bounded by small constant", s.checked, m)
			}
		})
	}
}
