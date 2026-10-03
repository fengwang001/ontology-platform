package flow

import (
	"errors"
	"testing"
)

func TestDefineOK(t *testing.T) {
	c := NewCatalog()
	if err := c.Define([]byte("d"), 0b0110, []uint64{0b0010, 0b0100}); err != nil {
		t.Fatalf("Define 意外错误: %v", err)
	}
	d, ok := c.Get([]byte("d"))
	if !ok || d.Ceil != 0b0110 || len(d.Reqs) != 2 {
		t.Fatalf("Get = %+v ok=%v", d, ok)
	}
	t.Logf("输入 Define(ceil=0b0110, reqs=[0b0010,0b0100]) → 输出 %+v；判定依据各项非零且为 ceil 子集", d)
}

func TestDefineErrors(t *testing.T) {
	const bit63 = uint64(1) << 63
	cases := []struct {
		name string
		def  []byte
		ceil uint64
		reqs []uint64
		want error
	}{
		{"空定义名", nil, 0b11, []uint64{0b01}, ErrArg},
		{"ceil含位63", []byte("d"), bit63, []uint64{bit63}, ErrDef},
		{"零步骤", []byte("d"), 0b11, nil, ErrDef},
		{"17步", []byte("d"), 0xFFFFFFFFFFFFFFFF >> 1, bigReqs(), ErrDef},
		{"零需求", []byte("d"), 0b11, []uint64{0}, ErrDef},
		{"需求越界", []byte("d"), 0b0110, []uint64{0b1001}, ErrDef},
	}
	c := NewCatalog()
	for _, tc := range cases {
		err := c.Define(tc.def, tc.ceil, tc.reqs)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, 期望 %v", tc.name, err, tc.want)
		}
		t.Logf("输入 %s → %v；判定依据 %s", tc.name, err, tc.name)
	}
}

func TestGetMissingAndCopy(t *testing.T) {
	c := NewCatalog()
	if _, ok := c.Get([]byte("x")); ok {
		t.Fatal("不存在的定义应返回 ok=false")
	}
	mustDefine(t, c)
	d, _ := c.Get([]byte("d"))
	d.Reqs[0] = 999
	d2, _ := c.Get([]byte("d"))
	if d2.Reqs[0] == 999 {
		t.Fatal("Get 返回的切片应为副本，外部修改不得污染定义表")
	}
}

func bigReqs() []uint64 {
	r := make([]uint64, 17)
	for i := range r {
		r[i] = 1
	}
	return r
}

func mustDefine(t *testing.T, c *Catalog) {
	t.Helper()
	if err := c.Define([]byte("d"), 0b11, []uint64{1}); err != nil {
		t.Fatal(err)
	}
}
