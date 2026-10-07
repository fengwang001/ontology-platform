package ontology

import "testing"

// 基数判定开销不随现有关联总数增长：同一“添加 1 条关联”的判定，
// 在已有 10 与已有 100_000 条关联时耗时应处于同一量级。
// 证据形式：go test -bench=BenchmarkProjectViolations -benchmem，
// 比较两个规模的 ns/op（判定只遍历本次操作触及的约束键，
// 当前数量取 map 的 len）。
func BenchmarkProjectViolations(b *testing.B) {
	for _, existing := range []int{10, 100_000} {
		obj := &objectState{
			links:  map[string]map[string]bool{},
			limits: map[string]int{keyOf(testLink, Outgoing): existing + 1},
		}
		set := make(map[string]bool, existing)
		for i := 0; i < existing; i++ {
			set[itoa(i)] = true
		}
		obj.links[keyOf(testLink, Outgoing)] = set
		ops := []LinkOp{{LinkType: testLink, Direction: Outgoing, OtherID: "new", Add: true}}

		b.Run(existingName(existing), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if vs := projectViolations(obj, ops); len(vs) != 0 {
					b.Fatalf("unexpected violation: %+v", vs)
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

func existingName(n int) string {
	switch n {
	case 10:
		return "existing=10"
	default:
		return "existing=100000"
	}
}
