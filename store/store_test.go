package store

import (
	"sort"
	"testing"
)

func TestPutGet(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value []byte
	}{
		{"normal", "a", []byte("1")},
		{"empty key", "", []byte("v")},
		{"empty value", "b", []byte("")},
	}
	s := New()
	for _, c := range cases {
		s.Put(c.key, c.value)
		got, ok := s.Get(c.key)
		if !ok || string(got) != string(c.value) {
			t.Fatalf("%s: got %q ok=%v want %q", c.name, got, ok, c.value)
		}
	}
	if _, ok := s.Get("missing"); ok {
		t.Fatal("missing key must not exist")
	}
}

func TestSnapshotVersionedRead(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		initial string
		updated string
	}{
		{"rewritten", "k80", "old80", "new80"},
		{"empty value rewrite", "empty", "", "x"},
	}
	s := New()
	views := make([]*View, len(cases))
	for _, c := range cases {
		s.Put(c.key, []byte(c.initial))
	}
	for i := range cases {
		views[i] = s.View()
	}
	for _, c := range cases {
		s.Put(c.key, []byte(c.updated))
	}
	for i, c := range cases {
		if got, ok := views[i].Get(c.key); !ok || string(got) != c.initial {
			t.Fatalf("%s: snapshot saw %q ok=%v want %q", c.name, got, ok, c.initial)
		}
	}
	for _, v := range views {
		v.Close()
	}
	if got, ok := s.Get(cases[0].key); !ok || string(got) != cases[0].updated {
		t.Fatalf("live read: got %q ok=%v want %q", got, ok, cases[0].updated)
	}
}

func TestRetainAndRelease(t *testing.T) {
	s := New()
	for i := 0; i < 1000; i++ {
		s.Put(keyN(i), []byte("v"))
	}
	v1 := s.View()
	v2 := s.View() // 与 v1 重叠
	for i := 0; i < 100; i++ {
		s.Put(keyN(i), []byte("changed"))
	}
	if v1.Kept() != 100 || v2.Kept() != 100 {
		t.Fatalf("kept v1=%d v2=%d want 100", v1.Kept(), v2.Kept())
	}
	v1.Close()
	if v2.Kept() != 100 {
		t.Fatalf("v2 kept=%d, must retain until both closed", v2.Kept())
	}
	v2.Close()
	if got := len(s.views); got != 0 {
		t.Fatalf("active views after close=%d want 0", got)
	}
}

func TestKeptBoundedByChangedKeys(t *testing.T) {
	s := New()
	for i := 0; i < 100000; i++ {
		s.Put(keyN(i), []byte("v"))
	}
	v := s.View()
	for i := 0; i < 100; i++ {
		s.Put(keyN(i*7+3), []byte("x"))
	}
	if v.Kept() > 100 {
		t.Fatalf("kept=%d exceeds changed-key bound 100", v.Kept())
	}
	if got := v.Reads(); got != 0 {
		t.Fatalf("reads before export=%d want 0", got)
	}
	keys := v.Keys()
	if len(keys) != 100000 {
		t.Fatalf("visible keys=%d want 100000", len(keys))
	}
	sort.Strings(keys)
	for _, k := range keys {
		v.Get(k)
	}
	if v.Reads() != 100000 {
		t.Fatalf("reads=%d want one read per key (100000)", v.Reads())
	}
	v.Close()
}

func keyN(i int) string {
	const digits = "0123456789abcdef"
	b := make([]byte, 6)
	for j := 5; j >= 0; j-- {
		b[j] = digits[i%16]
		i /= 16
	}
	return string(b)
}
