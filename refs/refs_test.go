package refs

import (
	"errors"
	"reflect"
	"testing"

	"ontology/store"
)

func TestAddValidation(t *testing.T) {
	cases := []struct {
		name    string
		child   int64
		parent  int64
		policy  Policy
		wantErr error
	}{
		{"正常", 1, 2, Cascade, nil},
		{"自引用", 1, 1, Cascade, store.ErrInvalidArgument},
		{"非法策略负", 1, 2, Policy(-1), store.ErrInvalidArgument},
		{"非法策略超界", 1, 2, Policy(3), store.ErrInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := New()
			err := g.Add(tc.child, tc.parent, tc.policy)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Add = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestAddDuplicateAndOutLimit(t *testing.T) {
	g := New()
	for parent := int64(2); parent <= 9; parent++ {
		if err := g.Add(1, parent, SetNull); err != nil {
			t.Fatalf("第 %d 条出边应成功: %v", parent-1, err)
		}
	}
	if err := g.Add(1, 10, SetNull); !errors.Is(err, store.ErrTooManyOutgoing) {
		t.Fatalf("第 9 条出边 = %v, want ErrTooManyOutgoing", err)
	}
	if err := g.Add(1, 2, Restrict); !errors.Is(err, store.ErrExists) {
		t.Fatalf("重复边 = %v, want ErrExists", err)
	}
	if err := g.Add(2, 1, Cascade); err != nil {
		t.Fatalf("反向边应允许（成环）: %v", err)
	}
}

func TestRemoveAndIndexes(t *testing.T) {
	g := New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(g.Add(3, 1, Cascade))
	must(g.Add(2, 1, Restrict))
	must(g.Add(1, 4, SetNull))
	if got := g.In(1); !reflect.DeepEqual(got, []Edge{{Child: 2, Parent: 1}, {Child: 3, Parent: 1}}) {
		t.Fatalf("In(1) = %v", got)
	}
	if got := g.Out(1); !reflect.DeepEqual(got, []Edge{{Child: 1, Parent: 4}}) {
		t.Fatalf("Out(1) = %v", got)
	}
	if p, ok := g.Policy(2, 1); !ok || p != Restrict {
		t.Fatalf("Policy(2,1) = %v,%v", p, ok)
	}
	if err := g.Remove(9, 9); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Remove 缺失边 = %v, want ErrNotFound", err)
	}
	must(g.Remove(2, 1))
	if g.Has(2, 1) || g.Len() != 2 {
		t.Fatalf("Remove 后状态错误: has=%v len=%d", g.Has(2, 1), g.Len())
	}
	g.RemoveRecord(1)
	if g.Len() != 0 || len(g.In(4)) != 0 || len(g.Out(3)) != 0 {
		t.Fatalf("RemoveRecord 后残留: len=%d in4=%v out3=%v", g.Len(), g.In(4), g.Out(3))
	}
}
