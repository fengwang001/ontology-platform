package mapping

import (
	"errors"
	"strings"
	"testing"

	"ontology/coerce"
)

func TestNewValidation(t *testing.T) {
	if _, err := New(Mode(99), 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("mode: %v", err)
	}
	for _, fmax := range []int{0, -1, 100001} {
		if _, err := New(DynamicTrue, fmax); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("fmax=%d: %v", fmax, err)
		}
	}
}

func TestPutMapping(t *testing.T) {
	cases := []struct {
		name   string
		pre    [][]string // prior explicit paths, typed via preTyp
		preTyp coerce.Type
		path   []string
		typ    coerce.Type
		fmax   int
		errIs  error
		mv     int
		count  int
	}{
		{name: "simple add", path: []string{"a"}, typ: coerce.Long, fmax: 4, mv: 1, count: 1},
		{name: "parents auto object", path: []string{"a", "b", "c"}, typ: coerce.Long, fmax: 4, mv: 1, count: 3},
		{
			name: "leaf parent conflict", preTyp: coerce.Long,
			pre:  [][]string{{"a"}},
			path: []string{"a", "b"},
			typ:  coerce.Long, fmax: 4, errIs: coerce.ErrTypeConflict, mv: 1, count: 1,
		},
		{
			name: "existing different type conflict", preTyp: coerce.Long,
			pre:  [][]string{{"a"}},
			path: []string{"a"}, typ: coerce.Keyword, fmax: 4,
			errIs: coerce.ErrTypeConflict, mv: 1, count: 1,
		},
		{
			name: "same type no-op", preTyp: coerce.Long,
			pre:  [][]string{{"a"}},
			path: []string{"a"}, typ: coerce.Long, fmax: 4, mv: 1, count: 1,
		},
		{
			name: "same type nested no-op", preTyp: coerce.Long,
			pre:  [][]string{{"a", "b"}},
			path: []string{"a", "b"}, typ: coerce.Long, fmax: 4, mv: 1, count: 2,
		},
		{name: "field limit exactly", path: []string{"a", "b", "c"}, typ: coerce.Long, fmax: 3, mv: 1, count: 3},
		{name: "field limit over", path: []string{"a", "b", "c"}, typ: coerce.Long, fmax: 2, errIs: ErrFieldLimit, mv: 0, count: 0},
		{name: "empty path", path: nil, typ: coerce.Long, fmax: 4, errIs: ErrInvalidArgument},
		{name: "dotted key", path: []string{"a.b"}, typ: coerce.Long, fmax: 4, errIs: ErrInvalidArgument},
		{name: "long key", path: []string{strings.Repeat("k", 65)}, typ: coerce.Long, fmax: 4, errIs: ErrInvalidArgument},
		{name: "bad type", path: []string{"a"}, typ: coerce.Type("wat"), fmax: 4, errIs: ErrInvalidArgument},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := New(DynamicTrue, c.fmax)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range c.pre {
				if err := m.PutMapping(p, coerce.Long); err != nil {
					t.Fatal(err)
				}
			}
			err = m.PutMapping(c.path, c.typ)
			if c.errIs != nil {
				if !errors.Is(err, c.errIs) {
					t.Fatalf("err = %v, want %v", err, c.errIs)
				}
			} else if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if m.MV() != c.mv || m.FieldCount() != c.count {
				t.Fatalf("mv=%d count=%d want %d,%d", m.MV(), m.FieldCount(), c.mv, c.count)
			}
		})
	}
}

func TestRollbackRemovesTentativeNodes(t *testing.T) {
	m, _ := New(DynamicTrue, 3)
	tx := m.Begin()
	if _, err := tx.Create(nil, "a", coerce.Object); err != nil {
		t.Fatal(err)
	}
	_, level, _ := tx.Lookup(nil, "a")
	if _, err := tx.Create(level, "b", coerce.Long); err != nil {
		t.Fatal(err)
	}
	if tx.Count() != 2 {
		t.Fatalf("count during tx = %d", tx.Count())
	}
	tx.Rollback()
	if m.FieldCount() != 0 || m.MV() != 0 {
		t.Fatalf("after rollback count=%d mv=%d", m.FieldCount(), m.MV())
	}
	snap := m.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("snapshot not empty: %v", snap)
	}
}

func TestTouchedIndependentOfFieldTotal(t *testing.T) {
	for _, total := range []int{10, 10000} {
		m, _ := New(DynamicTrue, total+10)
		tx := m.Begin()
		for i := 0; i < total; i++ {
			key := "f" + itoaPad(i, 5)
			if _, err := tx.Create(nil, key, coerce.Long); err != nil {
				t.Fatal(err)
			}
		}
		objLevel, err := tx.Create(nil, "zzz-doc-obj", coerce.Object)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Create(objLevel, "x", coerce.Long); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Create(objLevel, "y", coerce.Long); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Create(nil, "zzz-doc-a", coerce.Long); err != nil {
			t.Fatal(err)
		}
		tx.Commit()

		walk := m.Begin()
		// Re-indexing one document touches exactly its own nodes:
		// obj, obj.x, obj.y, a = 4 nodes, never the f00000.. noise.
		_, docObj, ok := walk.Lookup(nil, "zzz-doc-obj")
		if !ok {
			t.Fatal("obj missing")
		}
		_, _, _ = walk.Lookup(docObj, "x")
		_, _, _ = walk.Lookup(docObj, "y")
		_, _, _ = walk.Lookup(nil, "zzz-doc-a")
		if got := walk.Touched(); got != 4 {
			t.Fatalf("total=%d touched=%d, want <= 5", total, got)
		}
		walk.Rollback()
	}
}

func itoaPad(n, width int) string {
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	for len(b) < width {
		b = append([]byte{'0'}, b...)
	}
	return string(b)
}
