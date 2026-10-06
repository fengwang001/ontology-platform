package surge

import "testing"

// BenchmarkEval 证明一次区域评估的耗时不随该区域骑手数增长：
// 子用例骑手/订单规模扩大 10 倍，评估只读取两个增量计数。
func BenchmarkEval(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(itoa(n), func(b *testing.B) {
			s := newSysT(&testing.T{})
			for i := 0; i < n; i++ {
				id := "x" + itoa(i)
				_ = s.AddRider(id)
				if e := s.RiderOnline(id, "A", int64(i+1)); e != nil {
					b.Fatal(e)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if e := s.Evaluate("A", int64(n+1+i*5)); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}

// BenchmarkDispatchComplete 证明派单/完成耗时不随平台订单总量增长：
// 预先创建 n 个订单，反复对固定新订单做派单+完成（订单按 id 哈希定位）。
func BenchmarkDispatchComplete(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(itoa(n), func(b *testing.B) {
			s := newSysT(&testing.T{})
			_ = s.RiderOnline("r1", "A", 0)
			for i := 0; i < n; i++ {
				if _, e := s.CreateOrder("z"+itoa(i), "A", int64(i+1)); e != nil {
					b.Fatal(e)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				oid := "live" + itoa(i)
				at := int64(n + 2 + i*3)
				if _, e := s.CreateOrder(oid, "A", at); e != nil {
					b.Fatal(e)
				}
				if e := s.DispatchOrder(oid, "r1", at+1); e != nil {
					b.Fatal(e)
				}
				if _, _, e := s.CompleteOrder(oid, at+2); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}
