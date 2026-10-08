package constexpr

import "testing"

// BenchmarkLookupIndependentOfSize：Lookup 为 map 直达，O(1)，
// 与已登记常量总数无关。
func BenchmarkLookupIndependentOfSize(b *testing.B) {
	for _, n := range []int{100, 10_000} {
		r := NewRegistry()
		for i := 0; i < n; i++ {
			name := "c" + itoa(i)
			if err := r.Register(name, IntLit(bi("1"))); err != nil {
				b.Fatal(err)
			}
		}
		target := "c" + itoa(n-1)
		b.Run(itoa(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, ok := r.Lookup(target); !ok {
					b.Fatal("missing")
				}
			}
		})
	}
}

// BenchmarkRefIndependentOfChain：命名常量在登记时已求定并固定，
// 引用只做一次 map 查找，不沿引用链展开。
func BenchmarkRefIndependentOfChain(b *testing.B) {
	for _, depth := range []int{10, 1000} {
		r := NewRegistry()
		if err := r.Register("c0", IntLit(bi("1"))); err != nil {
			b.Fatal(err)
		}
		for i := 1; i <= depth; i++ {
			if err := r.Register("c"+itoa(i), Binary(OpAdd, Ref("c"+itoa(i-1)), IntLit(bi("1")))); err != nil {
				b.Fatal(err)
			}
		}
		target := Ref("c" + itoa(depth))
		b.Run(itoa(depth), func(b *testing.B) {
			ev := r.NewEvaluator()
			for i := 0; i < b.N; i++ {
				if _, err := ev.Eval(target); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		buf[p] = '-'
	}
	return string(buf[p:])
}
