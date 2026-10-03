package store

import (
	"encoding/binary"
	"testing"
)

func key(k uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, k)
	return b
}

func TestPutGetDeleteOrder(t *testing.T) {
	s := New("t")
	if ok := s.Put(key(0x0200), 20); ok {
		t.Fatal("empty store Put must not overwrite")
	}
	s.Put(key(0x0100), 10)
	s.Put(key(0x0300), 30)
	if v, ok := s.Get(key(0x0100)); !ok || v != 10 {
		t.Fatalf("Get = %d,%v want 10,true", v, ok)
	}
	if !s.Put(key(0x0100), 11) {
		t.Fatal("existing key Put must report overwrite")
	}
	if v, _ := s.Get(key(0x0100)); v != 11 {
		t.Fatalf("overwrite failed, got %d", v)
	}
	if s.Len() != 3 {
		t.Fatalf("Len = %d want 3", s.Len())
	}

	var cnt uint64
	all := s.Scan(nil, nil, &cnt)
	if len(all) != 3 || all[0].Value != 11 || all[2].Value != 30 {
		t.Fatalf("full scan order wrong: %+v", all)
	}
	if cnt != 3 {
		t.Fatalf("counter = %d want 3", cnt)
	}

	half := s.ScanN(key(0x0100), key(0x0200), -1, nil)
	if len(half) != 1 || half[0].Value != 11 {
		t.Fatalf("range scan [0x0100,0x0300) = %+v", half)
	}
	lim := s.ScanN(key(0), nil, 2, nil)
	if len(lim) != 2 {
		t.Fatalf("limit scan len = %d want 2", len(lim))
	}

	if !s.Delete(key(0x0200)) || s.Delete(key(0x0200)) {
		t.Fatal("Delete existence reporting wrong")
	}
	if s.Len() != 2 {
		t.Fatalf("Len after delete = %d want 2", s.Len())
	}
}

func TestScanBoundaryUnsigned(t *testing.T) {
	s := New("t")
	s.Put(key(0x8000000000000000), -1)
	s.Put(key(0x7fffffffffffffff), 1)
	got := s.Scan(key(0x8000000000000000), nil, nil)
	if len(got) != 1 || got[0].Value != -1 {
		t.Fatalf("unsigned high-half scan = %+v", got)
	}
}
