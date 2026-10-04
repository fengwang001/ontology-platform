package change

import "testing"

func TestTripleOrdering(t *testing.T) {
	cases := []struct {
		a, b Triple
		want bool // a < b
	}{
		{Triple{10, 1, 1}, Triple{10, 2, 1}, true},  // 同 ts 按 origin
		{Triple{10, 2, 1}, Triple{10, 1, 1}, false}, // 对称
		{Triple{10, 1, 1}, Triple{10, 1, 2}, true},  // 同 ts 同 origin 按 seq
		{Triple{5, 9, 9}, Triple{10, 1, 1}, true},   // ts 优先
		{Triple{10, 1, 1}, Triple{10, 1, 1}, false}, // 相等不小于
	}
	for _, c := range cases {
		if got := c.a.Less(c.b); got != c.want {
			t.Fatalf("%v.Less(%v)=%v, 期望 %v", c.a, c.b, got, c.want)
		}
		t.Logf("输入 %v 与 %v 输出 Less=%v 判定依据: (ts,origin,seq) 字典序", c.a, c.b, c.a.Less(c.b))
	}
}

func TestValidation(t *testing.T) {
	if !ValidKey("k") || ValidKey("") || ValidKey(string(make([]byte, 33))) || !ValidKey(string(make([]byte, 32))) {
		t.Fatalf("键合法性判定错误")
	}
	if !ValidNow(0) || !ValidNow(MaxNow) || ValidNow(-1) || ValidNow(MaxNow+1) {
		t.Fatalf("now 合法性判定错误")
	}
	if !Put.Valid() || !Del.Valid() || Op(3).Valid() {
		t.Fatalf("op 合法性判定错误")
	}
}
