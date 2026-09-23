package segment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type kv struct {
	key, val string
	del      bool
}

func build(t *testing.T, entries []kv) *Reader {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.seg")
	w, err := NewWriter(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		var err error
		if e.del {
			err = w.Delete([]byte(e.key))
		} else {
			err = w.Add([]byte(e.key), []byte(e.val))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestGet(t *testing.T) {
	cases := []struct {
		name    string
		entries []kv
		key     string
		want    State
		wantVal string
	}{
		{"empty segment", nil, "a", StateAbsent, ""},
		{"single hit", []kv{{"a", "1", false}}, "a", StateValue, "1"},
		{"single miss", []kv{{"a", "1", false}}, "b", StateAbsent, ""},
		{"empty key legal", []kv{{"", "v", false}}, "", StateValue, "v"},
		{"empty value legal", []kv{{"k", "", false}}, "k", StateValue, ""},
		{"tombstone", []kv{{"k", "", true}}, "k", StateDeleted, ""},
		{"tombstone not absent", []kv{{"a", "1", false}, {"b", "", true}}, "b", StateDeleted, ""},
		{"miss between keys", []kv{{"a", "1", false}, {"c", "3", false}}, "b", StateAbsent, ""},
		{"miss before all", []kv{{"b", "2", false}}, "a", StateAbsent, ""},
		{"miss after all", []kv{{"b", "2", false}}, "z", StateAbsent, ""},
		{"last entry", []kv{{"a", "1", false}, {"b", "2", false}, {"c", "3", false}}, "c", StateValue, "3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := build(t, c.entries)
			val, st, err := r.Get([]byte(c.key))
			if err != nil {
				t.Fatal(err)
			}
			if st != c.want || string(val) != c.wantVal {
				t.Fatalf("Get(%q) = (%q,%v), want (%q,%v)", c.key, val, st, c.wantVal, c.want)
			}
		})
	}
}

func TestGetCompareBound(t *testing.T) {
	const n = 100000
	p := filepath.Join(t.TempDir(), "big.seg")
	w, _ := NewWriter(p)
	for i := 0; i < n; i++ {
		if err := w.Add([]byte(fmt.Sprintf("key%08d", i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	const bound = 4 * 17 // 4*ceil(log2(100000))
	for _, key := range []string{"key00000000", "key00050000", "key00099999", "key00100000", "zzz"} {
		r.ResetCompares()
		if _, _, err := r.Get([]byte(key)); err != nil {
			t.Fatal(err)
		}
		if got := r.Compares(); got > bound {
			t.Fatalf("Get(%q) compares=%d > bound %d", key, got, bound)
		}
	}
}

func TestWriterOrder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.seg")
	w, _ := NewWriter(p)
	defer w.Close()
	if err := w.Add([]byte("b"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := w.Add([]byte("b"), []byte("2")); err != nil { // 同键允许（多版本）
		t.Fatal(err)
	}
	if err := w.Add([]byte("a"), []byte("3")); !errors.Is(err, ErrUnsorted) {
		t.Fatalf("want ErrUnsorted, got %v", err)
	}
}

func TestRecoverPrefixDropsHalfEntry(t *testing.T) {
	entries := []kv{{"aa", "1", false}, {"bb", "2", false}, {"cc", "3", false}}
	r := build(t, entries)
	p := r.f.Name()
	r.Close()
	full, err := RecoverPrefix(p)
	if err != nil || len(full) != 3 {
		t.Fatalf("full recover = %d, %v", len(full), err)
	}
	// 每条 15B，条目区 [36,81)：截到 76 即切在第 3 条中间，前缀应恰为前 2 条。
	if err := os.Truncate(p, 76); err != nil {
		t.Fatal(err)
	}
	got, err := RecoverPrefix(p)
	if err != nil || len(got) != 2 || string(got[1].Key) != "bb" {
		t.Fatalf("truncated recover = %v, %v", got, err)
	}
}
