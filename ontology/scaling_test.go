package ontology

import (
	"fmt"
	"testing"
)

// buildFactScaledStore 构造一个对象：V 个有效时间，每个有效时间
// C 条修正，共 V*C 条事实。
func buildFactScaledStore(tb testing.TB, validTimes, corrections int) *Store {
	tb.Helper()
	s := NewStore()
	if err := s.CreateObjectType("T", simpleProps(), 0); err != nil {
		tb.Fatal(err)
	}
	if err := s.RegisterObject("T", "o1"); err != nil {
		tb.Fatal(err)
	}
	rt := RecordTime(1)
	for vt := 0; vt < validTimes; vt++ {
		for c := 0; c < corrections; c++ {
			if err := s.WriteFact(Fact{
				ObjectID:   "o1",
				ValidTime:  ValidTime(vt),
				RecordTime: rt,
				Values:     map[string]Value{"a": IntValue(int64(c))},
			}); err != nil {
				tb.Fatal(err)
			}
			rt++
		}
	}
	return s
}

// expandWindowProbes 在固定宽度窗口上展开并返回探针计数。
func expandWindowProbes(tb testing.TB, s *Store, from, to ValidTime) Stats {
	tb.Helper()
	s.ResetStats()
	res, err := s.Expand(ExpandRequest{
		ObjectID: "o1", AsOfRecord: RecordTime(1 << 40),
		ValidFrom: from, ValidTo: to,
	})
	if err != nil {
		tb.Fatal(err)
	}
	if len(res.Facts) == 0 {
		tb.Fatal("empty expansion")
	}
	return s.Stats()
}

// TestExpansionSublinearInFacts 验证展开开销不随历史事实总量线性增长：
// 事实总量放大 16 倍时，固定窗口展开的探针计数增量仅为对数级。
// 探针计数是确定性的，可独立复核，不依赖墙钟。
func TestExpansionSublinearInFacts(t *testing.T) {
	const corrections = 8
	const window = 100 // 固定窗口：100 个有效时间

	small := buildFactScaledStore(t, 2_000, corrections)  // 16k 事实
	large := buildFactScaledStore(t, 32_000, corrections) // 256k 事实（16x）

	// 窗口位于两部历史的中部，避开边界效应。
	smallStats := expandWindowProbes(t, small, 1000, 1000+window-1)
	largeStats := expandWindowProbes(t, large, 16000, 16000+window-1)

	// 对数增长上界：log2(16) = 4，每项搜索增量不超过 4 次比较，
	// 预留充分余量（窗口内 100 个有效时间 * 每项 4 + 常量 64）。
	factBound := smallStats.FactProbes + window*4 + 64
	if largeStats.FactProbes > factBound {
		t.Fatalf("fact probes grew super-logarithmically: %d -> %d (bound %d)",
			smallStats.FactProbes, largeStats.FactProbes, factBound)
	}
	t.Logf("fact probes: %d (16k facts) -> %d (256k facts), bound %d",
		smallStats.FactProbes, largeStats.FactProbes, factBound)
}

// TestExpansionSublinearInMigrations 验证展开开销不随迁移总次数线性增长。
func TestExpansionSublinearInMigrations(t *testing.T) {
	build := func(migrations int) *Store {
		s := NewStore()
		if err := s.CreateObjectType("T", simpleProps(), 0); err != nil {
			t.Fatal(err)
		}
		if err := s.RegisterObject("T", "o1"); err != nil {
			t.Fatal(err)
		}
		// 先写事实（记录时刻远小于所有迁移生效时刻）。
		for vt := 0; vt < 200; vt++ {
			if err := s.WriteFact(Fact{
				ObjectID: "o1", ValidTime: ValidTime(vt), RecordTime: RecordTime(vt + 1),
				Values: map[string]Value{"a": IntValue(1)},
			}); err != nil {
				t.Fatal(err)
			}
		}
		// 再执行 migrations 次迁移，生效时刻递增且互不遮蔽。
		for m := 0; m < migrations; m++ {
			if err := s.Migrate(Migration{
				TypeID:        "T",
				EffectiveFrom: RecordTime(1000 + m),
				NewProps: map[string]PropertyDef{
					"a":                   {Name: "a", Type: TypeInt, Required: true},
					fmt.Sprintf("p%d", m): {Name: fmt.Sprintf("p%d", m), Type: TypeInt, Required: false},
				},
			}); err != nil {
				t.Fatal(err)
			}
		}
		return s
	}

	small := build(8)
	large := build(128) // 16x 迁移次数

	smallStats := expandWindowProbes(t, small, 0, 199)
	largeStats := expandWindowProbes(t, large, 0, 199)

	// 版本二分查找增量上界：每条事实 log2(16) = 4 次比较 + 余量。
	versionBound := smallStats.VersionProbes + 200*4 + 64
	if largeStats.VersionProbes > versionBound {
		t.Fatalf("version probes grew super-logarithmically: %d -> %d (bound %d)",
			smallStats.VersionProbes, largeStats.VersionProbes, versionBound)
	}
	t.Logf("version probes: %d (8 migrations) -> %d (128 migrations), bound %d",
		smallStats.VersionProbes, largeStats.VersionProbes, versionBound)
}

// BenchmarkExpandScaling 提供墙钟证据：历史总量放大 16 倍，
// 固定窗口展开耗时应近似不变（对数级）。
func BenchmarkExpandScaling(b *testing.B) {
	for _, validTimes := range []int{2_000, 32_000} {
		s := buildFactScaledStore(b, validTimes, 8)
		from := ValidTime(validTimes / 2)
		b.Run(fmt.Sprintf("facts=%d", validTimes*8), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := s.Expand(ExpandRequest{
					ObjectID: "o1", AsOfRecord: RecordTime(1 << 40),
					ValidFrom: from, ValidTo: from + 99,
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
