package ontology

import "testing"

// 判定一次占用申请/普通更新的开销不得随系统实例总数或占用记录总数增长。
// 该基准给出可验证证据：判定是对目标键的 O(1) map 访问，与 n 无关。
func BenchmarkAcquireVsScale(b *testing.B) {
	for _, n := range []int{100, 10_000, 100_000} {
		b.Run(itoa(n), func(b *testing.B) {
			s, _ := newTestStore()
			keys := make([]string, n)
			for i := 0; i < n; i++ {
				k := "inst-" + itoa(i)
				keys[i] = k
				s.CreateInstance(k, "open", nil)
			}
			// 让系统中并存 n/10 条占用记录，扩大“占用记录总数”。
			for i := 0; i+1 < n; i += 10 {
				l, out := s.TryAcquire("background-"+itoa(i), []string{keys[i+1]}, 1_000_000_000)
				if out != OutcomeCommitted {
					b.Fatal(out)
				}
				defer l.Release()
			}
			target := keys[0]
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				l, out := s.TryAcquire("bench", []string{target}, 1_000_000_000)
				if out != OutcomeCommitted {
					b.Fatal(out)
				}
				l.Release()
			}
		})
	}
}

func BenchmarkUpdateVsScale(b *testing.B) {
	for _, n := range []int{100, 10_000, 100_000} {
		b.Run(itoa(n), func(b *testing.B) {
			s, _ := newTestStore()
			target := "inst-0"
			for i := 0; i < n; i++ {
				s.CreateInstance("inst-"+itoa(i), "open", nil)
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				snap, _ := s.Snapshot(target)
				if out := s.Update("bench", target, snap.Version, Patch{"k": "v"}); out != OutcomeCommitted {
					b.Fatal(out)
				}
			}
		})
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
