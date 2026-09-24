package union

import "testing"

// 造 m 段两两隔开的区间 [10k,10k+5)。
func filled(m int) *Set {
	u := New(m + 1)
	for k := 0; k < m; k++ {
		if _, err := u.Add(int64(10*k), int64(10*k+5)); err != nil {
			panic(err)
		}
	}
	return u
}

// TestCheckedNotLinear 钉住定位复杂度：Withdraw 只严格重叠一段时，
// 线性检查个数是不随 m 增长的小常数（命中段 + 扫描终止段）。
func TestCheckedNotLinear(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		u := filled(m)
		k := m / 2
		if _, err := u.Withdraw(int64(10*k+2), int64(10*k+3)); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if u.checked > 4 { // 真正被拆分的 1 段 + 终止段，与 m 无关
			t.Fatalf("m=%d: checked=%d 随 m 线性增长", m, u.checked)
		}
	}
}

// TestCheckedAddLocal 钉住 Add 同样只做局部扫描。
func TestCheckedAddLocal(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		u := filled(m)
		k := m / 2
		if _, err := u.Add(int64(10*k+5), int64(10*k+10)); err != nil { // 邻接左右两段
			t.Fatalf("m=%d: %v", m, err)
		}
		if u.checked > 5 { // 接触的 2 段 + 终止段，与 m 无关
			t.Fatalf("m=%d: checked=%d 随 m 线性增长", m, u.checked)
		}
	}
}

// TestSelfCheck 包内自检（多档 m）。
func TestSelfCheck(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
