package txid

import "testing"

func TestCounterMonotonic(t *testing.T) {
	c := NewCounter()
	if got := c.Current(); got != Invalid {
		t.Fatalf("初始 Current 应为 Invalid，得到 %v", got)
	}
	prev := Invalid
	for i := 0; i < 1000; i++ {
		id, err := c.Next()
		if err != nil {
			t.Fatalf("Next 出错: %v", err)
		}
		if !id.Valid() {
			t.Fatalf("分配到非法零值")
		}
		if !prev.Before(id) {
			t.Fatalf("非单调: %v 之后 %v", prev, id)
		}
		prev = id
	}
	if c.Current() != prev {
		t.Fatalf("Current 应为最后分配值")
	}
}

func TestZeroInvalid(t *testing.T) {
	var zero ID
	if zero.Valid() {
		t.Fatalf("零值必须非法")
	}
	if zero != Invalid {
		t.Fatalf("零值应等于 Invalid")
	}
}

func TestCompare(t *testing.T) {
	a, b := ID(3), ID(7)
	if !a.Before(b) || !b.After(a) || a.After(b) || b.Before(a) {
		t.Fatalf("比较语义错误")
	}
	if a.Before(a) || a.After(a) {
		t.Fatalf("自比较必须严格")
	}
}
