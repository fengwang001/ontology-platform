package grants

import (
	"errors"
	"testing"
)

func TestGrantRevokeMask(t *testing.T) {
	r := New()
	p := []byte("p")
	if got := r.Mask(p); got != 0 {
		t.Fatalf("默认掩码 = %#b, 期望 0", got)
	}
	must(t, r.Grant(p, 0b0111))
	if got := r.Mask(p); got != 0b0111 {
		t.Fatalf("Grant 后掩码 = %#b, 期望 0b0111", got)
	}
	t.Logf("输入 Grant(p,0b0111) → 输出 %#b；判定依据 `|=`", r.Mask(p))
	must(t, r.Revoke(p, 0b0010))
	if got := r.Mask(p); got != 0b0101 {
		t.Fatalf("Revoke 后掩码 = %#b, 期望 0b0101", got)
	}
	t.Logf("输入 Revoke(p,0b0010) → 输出 %#b；判定依据 `&^=`", r.Mask(p))
	must(t, r.Revoke(p, 0b0010)) // 幂等
	if got := r.Mask(p); got != 0b0101 {
		t.Fatalf("重复 Revoke 后掩码 = %#b, 期望 0b0101", got)
	}
	must(t, r.Grant(p, ApproverBit))
	if r.Mask(p)&ApproverBit == 0 {
		t.Fatalf("位 63 Grant 失败: %#b", r.Mask(p))
	}
}

func TestMaskReadOnceCounter(t *testing.T) {
	r := New()
	before := r.reads
	_ = r.Mask([]byte("nobody"))
	_ = r.Mask([]byte("nobody"))
	if r.reads != before+2 {
		t.Fatalf("Mask 读取计数 = %d, 期望 %d（每次调用恰一次读）", r.reads, before+2)
	}
	t.Logf("输入两次 Mask → reads %d→%d；判定依据每次调用恰好一次读", before, r.reads)
}

func TestGrantArgs(t *testing.T) {
	r := New()
	for _, tc := range []struct {
		name string
		p    []byte
		m    uint64
	}{
		{"空主体-零掩码", nil, 1},
		{"零掩码", []byte("p"), 0},
	} {
		if err := r.Grant(tc.p, tc.m); !errors.Is(err, ErrArg) {
			t.Fatalf("%s: Grant err = %v, 期望 ErrArg", tc.name, err)
		}
		if err := r.Revoke(tc.p, tc.m); !errors.Is(err, ErrArg) {
			t.Fatalf("%s: Revoke err = %v, 期望 ErrArg", tc.name, err)
		}
		t.Logf("输入 %s → ErrArg（空主体或零掩码优先拒绝）", tc.name)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
}
