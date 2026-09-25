package ten

import (
	"fmt"
	"testing"
)

// TestTenantSemantics 表驱动钉住单租户语义：朴素增查删、更新不增 key、
// 两类配额判定顺序、删除回收计数、空 key/not found 哨兵、失败不留痕。
func TestTenantSemantics(t *testing.T) {
	const missing = "\x00"
	type op struct {
		do   func(tn *Tenant) error
		want error
	}
	cases := []struct {
		name   string
		mk, mb int
		ops    []op
		kc, tb int
		checks map[string]string
	}{
		{
			name: "put/update counts", mk: 2, mb: 100,
			ops: []op{
				{func(tn *Tenant) error { return tn.Put("x", "1") }, nil},
				{func(tn *Tenant) error { return tn.Put("y", "22") }, nil},
				{func(tn *Tenant) error { return tn.Put("x", "4444") }, nil},
			},
			kc: 2, tb: 6, checks: map[string]string{"x": "4444", "y": "22"},
		},
		{
			name: "maxkeys only blocks new key", mk: 1, mb: 100,
			ops: []op{
				{func(tn *Tenant) error { return tn.Put("x", "1") }, nil},
				{func(tn *Tenant) error { return tn.Put("y", "2") }, ErrQuotaKeys},
				{func(tn *Tenant) error { return tn.Put("x", "2") }, nil},
			},
			kc: 1, tb: 1, checks: map[string]string{"x": "2", "y": missing},
		},
		{
			name: "maxbytes new and update", mk: 10, mb: 3,
			ops: []op{
				{func(tn *Tenant) error { return tn.Put("x", "12") }, nil},
				{func(tn *Tenant) error { return tn.Put("y", "12") }, ErrQuotaBytes},
				{func(tn *Tenant) error { return tn.Put("x", "1234") }, ErrQuotaBytes},
				{func(tn *Tenant) error { return tn.Put("x", "3") }, nil},
			},
			kc: 1, tb: 1, checks: map[string]string{"x": "3", "y": missing},
		},
		{
			name: "del reclaims; empty/missing keys", mk: 5, mb: 50,
			ops: []op{
				{func(tn *Tenant) error { return tn.Put("x", "ab") }, nil},
				{func(tn *Tenant) error { return tn.Del("x") }, nil},
				{func(tn *Tenant) error { return tn.Del("x") }, ErrNotFound},
				{func(tn *Tenant) error { return tn.Del("") }, ErrEmptyKey},
				{func(tn *Tenant) error { return tn.Put("", "v") }, ErrEmptyKey},
				{func(tn *Tenant) error { _, e := tn.Get(""); return e }, ErrEmptyKey},
				{func(tn *Tenant) error { _, e := tn.Get("q"); return e }, ErrNotFound},
			},
			kc: 0, tb: 0, checks: map[string]string{"x": missing},
		},
		{
			name: "rejected put leaves no trace", mk: 1, mb: 2,
			ops: []op{
				{func(tn *Tenant) error { return tn.Put("x", "ab") }, nil},
				{func(tn *Tenant) error { return tn.Put("y", "c") }, ErrQuotaKeys},
				{func(tn *Tenant) error { return tn.Put("x", "xyz") }, ErrQuotaBytes},
			},
			kc: 1, tb: 2, checks: map[string]string{"x": "ab", "y": missing},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tn := New(c.mk, c.mb)
			for i, o := range c.ops {
				if err := o.do(tn); err != o.want {
					t.Fatalf("op %d: got %v want %v", i, err, o.want)
				}
			}
			if kc, tb := tn.Usage(); kc != c.kc || tb != c.tb {
				t.Fatalf("usage=(%d,%d) want (%d,%d)", kc, tb, c.kc, c.tb)
			}
			for k, want := range c.checks {
				got, err := tn.Get(k)
				if want == missing {
					if err != ErrNotFound {
						t.Fatalf("get %q: (%q,%v), want ErrNotFound", k, got, err)
					}
					continue
				}
				if err != nil || got != want {
					t.Fatalf("get %q=(%q,%v), want %q", k, got, err, want)
				}
			}
		})
	}
}

// TestQuotaCheckCountConstant 是复杂度证明（白盒）：m 取 100…10000 多档，
// 覆盖已存在 key 时最近一次配额检查检查过的 key 个数恒为 1（只看目标
// key），不随 m 线性增长。lastCheckedKeys 非导出，外部任何包读不到。
func TestQuotaCheckCountConstant(t *testing.T) {
	for _, m := range []int{100, 316, 1000, 3162, 10000} {
		tn := New(m+1, 1<<30)
		for i := 0; i < m; i++ {
			if err := tn.Put(fmt.Sprintf("k%d", i), "v"); err != nil {
				t.Fatalf("m=%d seed: %v", m, err)
			}
		}
		tn.lastCheckedKeys = 0
		target := fmt.Sprintf("k%d", m/2)
		if err := tn.Put(target, "w"); err != nil {
			t.Fatalf("m=%d overwrite: %v", m, err)
		}
		if tn.lastCheckedKeys != 1 {
			t.Fatalf("m=%d: checked %d keys, want constant 1", m, tn.lastCheckedKeys)
		}
	}
}
