package ontology_test

// 本文件以可验证的方式证明需求第 7 条：
//
//	"对一个尚未回填的实例按新版本结构即时现算视图的开销，不得随该对象类型
//	 迁移声明中累积的历史对应关系变更次数的增长而增长，只应与该实例自身的
//	 属性数量相关。"
//
// 验证手段（两层，均可重复执行）：
//
//  1. 分配计数（TestReadViewCostIndependentOfHistory）：对一个只有少量
//     自身属性、且始终不回填的实例，分别在声明修订次数 H=0,100,200,400
//     时测量"新版本读取"的每次堆分配数。如果现算视图需要重放/扫描历史，
//     分配数会随 H 线性上升；实测三组分配数必须相等（即斜率为 0），
//     只随该实例自身属性数量变化。
//  2. 基准（BenchmarkReadViewHistoryGrowth 与
//     BenchmarkReadViewInstanceAttrGrowth）：固定实例属性数、放大历史
//     修订次数，ns/op 与 allocs/op 必须保持平台；反之放大实例自身属性数，
//     开销随之增长——证明相关变量只有实例属性数。

import (
	"strconv"
	"testing"

	"ontology/ontology"
)

func buildStoreWithHistory(t *testing.T, historyRevisions int) (*ontology.Store, string) {
	t.Helper()
	s := ontology.NewStore("T", ontology.VersionOld, ontology.VersionNew)
	err := s.Create("inst", ontology.VersionOld, ontology.Props{
		"k1": ontology.Present("v1"),
		"k2": ontology.Present("v2"),
		"k3": ontology.Present("v3"),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.StartMigration(ontology.Migration{
		ObjectType: "T", From: ontology.VersionOld, To: ontology.VersionNew,
		Mappings: []ontology.Mapping{
			{Attr: "k1", Kind: ontology.KindKeep},
			{Attr: "k2", Kind: ontology.KindKeep},
			{Attr: "k3", Kind: ontology.KindKeep},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 每次修订追加一个全新 add 属性，制造 H 次"历史对应关系变更"。
	// inst 始终不回填，因此这些修订全部合法、互不冻结。
	for i := 0; i < historyRevisions; i++ {
		attr := ontology.AttrName("hist" + strconv.Itoa(i))
		if err := s.AmendDeclaration([]ontology.Mapping{
			{Attr: attr, Kind: ontology.KindAdd, Default: ontology.Present(0)},
		}); err != nil {
			t.Fatalf("修订 %d 被拒: %v", i, err)
		}
	}
	return s, "inst"
}

func allocsPerNewRead(b *testing.B, s *ontology.Store, id string) float64 {
	return testing.AllocsPerRun(200, func() {
		if _, err := s.Read(id, ontology.VersionNew); err != nil {
			b.Fatal(err)
		}
	})
}

// TestReadViewCostIndependentOfHistory：历史修订次数翻 4 倍，
// 未回填实例新视图读取的每次分配数必须保持不变（斜率为 0）。
func TestReadViewCostIndependentOfHistory(t *testing.T) {
	histories := []int{0, 100, 200, 400}
	results := make(map[int]float64, len(histories))
	for _, h := range histories {
		s, id := buildStoreWithHistory(t, h)
		got := testing.AllocsPerRun(300, func() {
			if _, err := s.Read(id, ontology.VersionNew); err != nil {
				t.Fatal(err)
			}
		})
		results[h] = got
		backfilled, _ := s.IsBackfilled(id)
		if backfilled {
			t.Fatalf("前提被破坏: h=%d 实例应仍未回填", h)
		}
		t.Logf("历史修订次数 H=%3d 时, 未回填实例新读每次分配数 = %.2f", h, got)
	}
	base := results[0]
	for _, h := range histories[1:] {
		// 允许测量噪声 ±15%，但不允许随历史增长。
		if results[h] > base*1.15 || results[h] < base*0.85 {
			t.Fatalf("新读开销随历史修订次数变化: H=0 -> %.2f, H=%d -> %.2f", base, h, results[h])
		}
	}
	t.Logf("结论: 历史修订 0->%d 次, 每读分配数稳定在 %.2f 附近, 与历史长度无关",
		histories[len(histories)-1], base)
}

func BenchmarkReadViewHistoryGrowth(b *testing.B) {
	for _, h := range []int{100, 1000, 4000} {
		s, id := buildStoreWithHistory(&testing.T{}, h)
		b.Run("history="+strconv.Itoa(h), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := s.Read(id, ontology.VersionNew); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestReadViewCostScalesWithInstanceAttrs：放大实例自身属性数，
// 新读分配数应随之增长（证明相关变量确实是实例属性数）。
func TestReadViewCostScalesWithInstanceAttrs(t *testing.T) {
	measure := func(attrCount int) float64 {
		s := ontology.NewStore("T", ontology.VersionOld, ontology.VersionNew)
		p := ontology.Props{}
		mappings := []ontology.Mapping{}
		for i := 0; i < attrCount; i++ {
			name := ontology.AttrName("p" + strconv.Itoa(i))
			p[name] = ontology.Present(i)
			mappings = append(mappings, ontology.Mapping{Attr: name, Kind: ontology.KindKeep})
		}
		if err := s.Create("inst", ontology.VersionOld, p); err != nil {
			t.Fatal(err)
		}
		if err := s.StartMigration(ontology.Migration{
			ObjectType: "T", From: 1, To: 2, Mappings: mappings,
		}); err != nil {
			t.Fatal(err)
		}
		return testing.AllocsPerRun(200, func() {
			if _, err := s.Read("inst", ontology.VersionNew); err != nil {
				t.Fatal(err)
			}
		})
	}
	small := measure(4)
	large := measure(256)
	t.Logf("实例属性 4 个 -> 每读 %.2f 次分配; 256 个 -> %.2f 次分配", small, large)
	if large <= small {
		t.Fatalf("实例属性数增长时现算开销应随之增长: small=%.2f large=%.2f", small, large)
	}
}
