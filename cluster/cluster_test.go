package cluster

import (
	"errors"
	"testing"
)

func TestRegistry(t *testing.T) {
	t.Run("join/leave 错误次序", func(t *testing.T) {
		r := New()
		cases := []struct {
			name string
			max  int
			want error
		}{
			{"a", 1, nil}, {"a", 1, ErrExists}, {"", 1, ErrInvalid},
			{"b", 0, ErrInvalid}, {"b", 4, ErrInvalid}, {"b", 3, nil},
		}
		for _, c := range cases {
			if got := r.Join(c.name, c.max); !errors.Is(got, c.want) {
				t.Fatalf("Join(%q,%d)=%v want %v", c.name, c.max, got, c.want)
			}
		}
		if got := r.Leave(""); !errors.Is(got, ErrInvalid) {
			t.Fatalf("Leave(空)=%v", got)
		}
		if got := r.Leave("z"); !errors.Is(got, ErrNotFound) {
			t.Fatalf("Leave(z)=%v", got)
		}
		if err := r.Leave("a"); err != nil {
			t.Fatal(err)
		}
		if err := r.Join("a", 2); err != nil { // 离开后同名可重新注册
			t.Fatalf("重新 Join: %v", err)
		}
	})

	t.Run("snapshot 与 minmax 并列", func(t *testing.T) {
		r := New()
		for _, c := range []struct {
			n string
			m int
		}{{"y", 3}, {"w", 2}, {"x", 2}} {
			if err := r.Join(c.n, c.m); err != nil {
				t.Fatal(err)
			}
		}
		got := r.Snapshot()
		if got[0].Name != "w" || got[1].Name != "x" || got[2].Name != "y" {
			t.Fatalf("Snapshot 未按字节序: %v", got)
		}
		min, who, ok := r.MinMax()
		if !ok || min != 2 || who != "w" {
			t.Fatalf("MinMax=(%d,%q,%v)", min, who, ok)
		}
		if err := r.Leave("w"); err != nil {
			t.Fatal(err)
		}
		if min, who, ok = r.MinMax(); !ok || min != 2 || who != "x" {
			t.Fatalf("Leave w 后 MinMax=(%d,%q,%v)", min, who, ok)
		}
		_ = r.Leave("x")
		_ = r.Leave("y")
		if _, _, ok := r.MinMax(); ok {
			t.Fatal("空注册表 MinMax 应返回 false")
		}
	})
}
