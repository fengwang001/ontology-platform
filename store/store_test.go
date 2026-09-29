package store_test

import (
	"errors"
	"testing"

	"ontology/addr"
	"ontology/store"
)

func TestDedupAndRefs(t *testing.T) {
	cases := []struct {
		name       string
		limit      store.Limits
		puts       [][]byte
		wantBlocks int
		wantBytes  int64
	}{
		{"dedup", store.Limits{}, [][]byte{[]byte("a"), []byte("a"), []byte("b")}, 2, 2},
		{"block cap", store.Limits{MaxBlocks: 2}, [][]byte{[]byte("a"), []byte("b")}, 2, 2},
		{"byte cap", store.Limits{MaxBytes: 4}, [][]byte{[]byte("ab"), []byte("cd")}, 2, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := store.New(tc.limit)
			var addrs []addr.Addr
			for _, p := range tc.puts {
				a, err := s.Put(p)
				if err != nil {
					t.Fatal(err)
				}
				addrs = append(addrs, a)
			}
			if s.BlockCount() != tc.wantBlocks || s.ByteCount() != tc.wantBytes {
				t.Fatalf("counts=%d,%d want %d,%d", s.BlockCount(), s.ByteCount(), tc.wantBlocks, tc.wantBytes)
			}
			if tc.name == "dedup" {
				if !addrs[0].Equal(addrs[1]) {
					t.Fatal("identical content got different addresses")
				}
				if s.Refs(addrs[0]) != 2 {
					t.Fatalf("refs=%d want 2", s.Refs(addrs[0]))
				}
				got, err := s.Get(addrs[0])
				if err != nil || string(got) != "a" {
					t.Fatal("get failed")
				}
			}
		})
	}
}

func TestErrorsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name  string
		limit store.Limits
		push  []byte
		want  error
	}{
		{"blocks", store.Limits{MaxBlocks: 1}, []byte("second"), store.ErrTooManyBlocks},
		{"bytes", store.Limits{MaxBytes: 1}, []byte("ab"), store.ErrTooManyBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := store.New(tc.limit)
			if _, err := s.Put([]byte("x")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Put(tc.push); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			if s.BlockCount() != 1 || s.Refs(addr.Of(tc.push)) != 0 {
				t.Fatal("rejected put left a trace")
			}
		})
	}
	_, err := store.New(store.Limits{}).Get(addr.Of([]byte("missing")))
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
}

func TestRelease(t *testing.T) {
	s := store.New(store.Limits{})
	a, _ := s.Put([]byte("z"))
	s.Put([]byte("z"))
	if err := s.Release(a); err != nil || s.Refs(a) != 1 || s.BlockCount() != 1 {
		t.Fatal("first release wrong")
	}
	if err := s.Release(a); err != nil || s.Refs(a) != 0 || s.BlockCount() != 0 {
		t.Fatal("second release must remove content")
	}
	if err := s.Release(a); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("release of missing address must fail")
	}
}
