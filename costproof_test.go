package ontology_test

import (
	"fmt"
	"testing"

	"ontology"
)

// 查询成本证明（规格要求「以可验证的方式证明」）：
//
// 在含 N 个实例（分布于多个分组）的类型上反复查询同一个分组，
// 真实子系统每次查询的 CostMeter 必须满足：
//   - InstanceRecordReads == 0（不读任何源实例记录）；
//   - GroupCellsTouched == 1（只触碰目标分组的增量单元）。
//
// 因此查询开销与类型下全部实例总数 N 无关，只与「该分组是否存在」相关
// （分组单元本身保存 Members，成员规模 O(1) 可得；若需要枚举成员则与
// 当前成员规模相关——视图不持久化成员列表快照，枚举由源实例点查完成）。
//
// 对照组：朴素模型每次查询扫描全部 N 条记录（Scanned == N），随 N 线性增长。
// 量化差距即「增量 O(1) vs 全扫 O(N)」的可执行证据；另有 BenchmarkQueryCost
// 在不同 N 下测量墙钟时间，给出同样结论。
func TestQueryCostIndependentOfTotalInstances(t *testing.T) {
	for _, n := range []int{100, 1000, 5000} {
		k2 := newKernel(t)
		m2 := newNaive(t)
		for i := 0; i < n; i++ {
			region := "bulk"
			if i%5 == 0 {
				region = "probe"
			}
			w := ontology.Write{Type: typeOrder, Key: fmt.Sprintf("n%d_k%d", n, i), Prev: 0,
				Attrs: attrs(1, region, "book")}
			if _, err := k2.Write(w); err != nil {
				t.Fatal(err)
			}
			if _, err := m2.Write(w); err != nil {
				t.Fatal(err)
			}
		}
		got, meter, _ := k2.Query(viewByRegion, "probe")
		want, scanned, _ := m2.Query(viewByRegion, "probe")
		trace(t, n, fmt.Sprintf("N=%d 实例下 Query(by_region, probe)", n),
			fmt.Sprintf("kernel: sum=%v members=%d 源实例读取=%d 分组单元触碰=%d | naive: sum=%v members=%d 扫描记录数=%d",
				got.Sum, got.Members, meter.InstanceRecordReads, meter.GroupCellsTouched,
				want.Sum, want.Members, scanned),
			"无论 N 如何增长，真实子系统读取源实例数恒为 0、触碰分组单元恒为 1；朴素模型扫描数恒等于 N；两边 SUM 相等")
		if meter.InstanceRecordReads != 0 {
			t.Fatalf("N=%d: query read %d source records, want 0", n, meter.InstanceRecordReads)
		}
		if meter.GroupCellsTouched != 1 {
			t.Fatalf("N=%d: query touched %d group cells, want 1", n, meter.GroupCellsTouched)
		}
		if scanned != n {
			t.Fatalf("naive control scanned %d, want %d", scanned, n)
		}
		if got.Sum != want.Sum || got.Members != want.Members {
			t.Fatalf("N=%d: result mismatch %+v vs %+v", n, got, want)
		}
	}
}

// BenchmarkQueryCost 证明墙钟开销：查询固定分组时，实例总数扩大 10 倍，
// 增量查询时间基本不变；朴素重扫时间随 N 线性放大。
// 运行: go test -run=^$ -bench=BenchmarkQueryCost -benchtime=1000x
func BenchmarkQueryCost(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		k := newKernel(b)
		m := newNaive(b)
		for i := 0; i < n; i++ {
			g := "bulk"
			if i == 0 {
				g = "probe"
			}
			w := ontology.Write{Type: typeOrder, Key: fmt.Sprintf("k%d", i), Prev: 0,
				Attrs: attrs(1, g, "book")}
			if _, err := k.Write(w); err != nil {
				b.Fatal(err)
			}
			if _, err := m.Write(w); err != nil {
				b.Fatal(err)
			}
		}
		b.Run(fmt.Sprintf("incremental/N=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				v, meter, _ := k.Query(viewByRegion, "probe")
				if meter.InstanceRecordReads != 0 || v.Sum != 1 {
					b.Fatal("bad query")
				}
			}
		})
		b.Run(fmt.Sprintf("naivescan/N=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				v, scanned, _ := m.Query(viewByRegion, "probe")
				if scanned != n || v.Sum != 1 {
					b.Fatal("bad scan")
				}
			}
		})
	}
}
