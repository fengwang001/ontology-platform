package schema

import (
	"errors"
	"testing"
)

func tf(name string, t Type) Field { return Field{Name: name, Type: t, Required: true} }

func TestNewVersionValidation(t *testing.T) {
	cases := []struct {
		name   string
		fields []Field
	}{
		{"empty", nil},
		{"too many", func() []Field {
			fs := make([]Field, 65)
			for i := range fs {
				fs[i] = tf(string(rune('a'+i%26)), Int32)
			}
			return fs
		}()},
		{"blank name", []Field{{Name: "", Type: Int32}}},
		{"bad type", []Field{tf("a", Type(99))}},
		{"duplicate name", []Field{tf("a", Int32), tf("a", Int64)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewVersion(c.fields); !errors.Is(err, ErrInvalidFields) {
				t.Fatalf("want ErrInvalidFields, got %v", err)
			}
		})
	}
}

func TestProjectionKeepsVersionOrder(t *testing.T) {
	v, err := NewVersion([]Field{tf("a", Int32), tf("b", Int64), tf("c", String)})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Project(v, []string{"c", "a"})
	if err != nil {
		t.Fatal(err)
	}
	got := p.Fields()
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("projection order = %+v, want [a c]", got)
	}
	if f, ok := p.FieldByName("c"); !ok || f.Type != String {
		t.Fatalf("FieldByName c = %+v,%v", f, ok)
	}
	for _, bad := range [][]string{
		nil, {"a", "a"}, {""}, {"a", "x"},
	} {
		if _, err := Project(v, bad); !errors.Is(err, ErrInvalidFields) {
			t.Fatalf("Project(%v): want ErrInvalidFields, got %v", bad, err)
		}
	}
}

func TestEqual(t *testing.T) {
	a, _ := NewVersion([]Field{tf("a", Int32), tf("b", String)})
	b, _ := NewVersion([]Field{tf("a", Int32), tf("b", String)})
	c, _ := NewVersion([]Field{tf("a", Int32), tf("b", Bytes)})
	if !a.Equal(b) || a.Equal(c) {
		t.Fatal("Equal mismatch")
	}
}
