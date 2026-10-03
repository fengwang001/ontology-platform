package cluster

import (
	"errors"
	"testing"
)

func TestRegistryTable(t *testing.T) {
	cases := []struct {
		name string
		run  func(r *Registry) error
		want error
	}{
		{"join 正常", func(r *Registry) error { return r.Join("a", 1) }, nil},
		{"join max=0", func(r *Registry) error { return r.Join("a", 0) }, ErrInvalidArg},
		{"join max=4", func(r *Registry) error { return r.Join("a", 4) }, ErrInvalidArg},
		{"join 空名", func(r *Registry) error { return r.Join("", 2) }, ErrInvalidArg},
		{"join 同名", func(r *Registry) error {
			if err := r.Join("a", 2); err != nil {
				return err
			}
			return r.Join("a", 3)
		}, ErrExists},
		{"leave 不存在", func(r *Registry) error { return r.Leave("x") }, ErrNotExist},
		{"leave 空名", func(r *Registry) error { return r.Leave("") }, ErrInvalidArg},
		{"leave 后同名重 join", func(r *Registry) error {
			if err := r.Join("a", 2); err != nil {
				return err
			}
			if err := r.Leave("a"); err != nil {
				return err
			}
			return r.Join("a", 3)
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			if err := tc.run(r); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestSnapshotSortedAndMinMax(t *testing.T) {
	r := New()
	if _, ok := r.MinMax(); ok {
		t.Fatal("空注册表 MinMax 应返回 false")
	}
	for _, n := range []struct {
		name string
		max  int
	}{{"c", 3}, {"a", 2}, {"b", 2}} {
		if err := r.Join(n.name, n.max); err != nil {
			t.Fatal(err)
		}
	}
	got := r.Snapshot()
	wantNames := []string{"a", "b", "c"}
	for i, n := range got {
		if n.Name != wantNames[i] {
			t.Fatalf("快照次序 = %v", got)
		}
	}
	min, ok := r.MinMax()
	if !ok || min != 2 {
		t.Fatalf("MinMax = %d,%v", min, ok)
	}
}
