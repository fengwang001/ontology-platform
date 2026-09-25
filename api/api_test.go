package api

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestViewAsOfConsistent(t *testing.T) {
	d := New()
	// (key, ts, value)；value=="<del>" 表示 Delete。乱序到达。
	type op struct {
		key string
		ts  int64
		val string
	}
	ops := []op{
		{"k1", 10, "a"}, {"k1", 30, "b"}, {"k1", 20, "c"}, {"k1", 40, "<del>"},
		{"k2", 5, "x"}, {"k2", 15, "y"},
		{"k3", 7, "p"}, {"k3", 9, "<del>"}, {"k3", 50, "q"},
	}
	for _, o := range ops {
		var err error
		if o.val == "<del>" {
			err = d.Delete(o.key, o.ts)
		} else {
			err = d.Write(o.key, o.ts, o.val)
		}
		if err != nil {
			t.Fatalf("op %+v: %v", o, err)
		}
	}
	for _, T := range []int64{0, 5, 8, 10, 15, 20, 30, 40, 50, 100} {
		// 重放参照：每个 key 取 ts<=T 中 ts 最大的操作，墓碑则不出现。
		latest := map[string]op{}
		for _, o := range ops {
			if o.ts <= T {
				if cur, ok := latest[o.key]; !ok || o.ts > cur.ts {
					latest[o.key] = o
				}
			}
		}
		want := map[string]string{}
		for k, o := range latest {
			if o.val != "<del>" {
				want[k] = o.val
			}
		}
		if got := d.ViewAsOf(T); !reflect.DeepEqual(got, want) {
			t.Errorf("ViewAsOf(%d)=%v，重放=%v", T, got, want)
		}
	}
}

func TestRejectedOpsLeaveStateUnchanged(t *testing.T) {
	cases := []struct {
		name string
		call func(d *DB) error
		want error
	}{
		{"空key写", func(d *DB) error { return d.Write("", 1, "v") }, ErrEmptyKey},
		{"空key删", func(d *DB) error { return d.Delete("", 1) }, ErrEmptyKey},
		{"负ts写", func(d *DB) error { return d.Write("k", -1, "v") }, ErrBadTime},
		{"负ts删", func(d *DB) error { return d.Delete("k", -1) }, ErrBadTime},
		{"空值写", func(d *DB) error { return d.Write("k", 1, "") }, ErrEmptyValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New()
			d.Write("k", 5, "keep")
			before := d.ViewAsOf(100)
			err := tc.call(d)
			if !errors.Is(err, tc.want) {
				t.Fatalf("错误=%v，应为 %v", err, tc.want)
			}
			if got := d.ViewAsOf(100); !reflect.DeepEqual(got, before) {
				t.Errorf("被拒后状态改变：%v -> %v", before, got)
			}
			if v, ok := d.AsOf("k", 5); !ok || v != "keep" { // 仍可正常使用
				t.Errorf("被拒后不可正常读取：%q/%v", v, ok)
			}
		})
	}
	// 三类错误互不相同
	if ErrEmptyKey == ErrBadTime || ErrBadTime == ErrEmptyValue || ErrEmptyKey == ErrEmptyValue {
		t.Fatal("三类哨兵错误必须互不相同")
	}
}

func TestConcurrentAsOfAPI(t *testing.T) {
	d := New()
	for ts := 1; ts <= 200; ts++ {
		d.Write("k", int64(ts), "v")
	}
	d.Write("other", 3, "z")
	const N = 32
	type res struct {
		s string
		b bool
	}
	results := make([]res, N)
	var wg sync.WaitGroup
	wg.Add(N)
	for g := 0; g < N; g++ {
		g := g
		go func() {
			defer wg.Done()
			v, ok := d.AsOf("k", 150)
			results[g] = res{v, ok}
		}()
	}
	wg.Wait()
	for _, r := range results {
		if !r.b || r.s != "v" {
			t.Fatalf("并发结果不一致：%+v", r)
		}
	}
	if err := d.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
