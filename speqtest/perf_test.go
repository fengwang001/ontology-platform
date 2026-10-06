package speqtest_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ontology/speq"
)

// 构造规模为 n 个设备的系统：
//   - 只有 hotCount 个对象的到期日落在查询窗口内（其余对象到期日远在未来）；
//   - 查询命中条数两档相同；若查询耗时不随 n 线性增长，
//     则证明预警查询只与命中条数 + 一个对数级因素相关。
func buildPerfSystem(t testing.TB, n int) (*speq.System, int) {
	s := speq.New()
	if err := s.AddCategory(speq.CategoryConfig{
		Code: "B", Kind: speq.KindDevice, PeriodMonths: 12,
		EarlyWindowDays: 30, MinUnsealDays: 1, WarningLeadDays: 10,
	}); err != nil {
		t.Fatal(err)
	}
	const hotCount = 20
	baseDate := 100000
	for i := 0; i < n; i++ {
		// 前 hotCount 个对象在最早期登记（之后被重算到查询窗口附近）；
		// 其余对象在较晚的时间登记，到期日彼此拉开、远离查询日。
		first := baseDate + 5000 + i*100
		if i < hotCount {
			first = baseDate - 5000
		}
		id := fmt.Sprintf("D%07d", i)
		if _, err := s.RegisterDevice(first, id, "B", first); err != nil {
			t.Fatal(err)
		}
	}
	// 用有条件合格（整改限期 5+i 天）把前 hotCount 个对象到期日压到查询日附近。
	// 注册完成后最后接受日期为 baseDate+5000+(n-1)*100；检验日须不小于它。
	queryDate := baseDate + 5000 + (n-1)*100 + 5
	for i := 0; i < hotCount; i++ {
		id := fmt.Sprintf("D%07d", i)
		// 首次检验 baseDate-5000，到期 baseDate-5000+365，远早于 queryDate => 已超期，
		// 合格规则基准 = 检验日。
		if _, err := s.Inspect(queryDate+i, id, speq.ResultConditional, 5+i); err != nil {
			t.Fatal(err)
		}
	}
	// 到期日分别为 queryDate+5 .. queryDate+24：
	// 查询日 queryDate、窗口 10 天：恰好命中 i=0..4（queryDate+5..+9）共 5 个。
	return s, queryDate
}

// TestPerfTwoTiers 两档对照：对象总数扩大 10 倍、命中数不变时，
// 查询耗时应基本持平（允许一定抖动，以 3 倍为保守上限）。
func TestPerfTwoTiers(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	type tier struct {
		n         int
		ns        int64
		hits      int
		depth     int
		indexSize int
	}
	var tiers [2]tier
	for ti, n := range [2]int{2000, 20000} {
		s, q := buildPerfSystem(t, n)
		// 预热。
		for i := 0; i < 200; i++ {
			if _, err := s.QueryWarnings(q); err != nil {
				t.Fatal(err)
			}
		}
		const iters = 20000
		start := time.Now()
		hits := -1
		for i := 0; i < iters; i++ {
			got, err := s.QueryWarnings(q)
			if err != nil {
				t.Fatal(err)
			}
			hits = len(got)
		}
		ns := time.Since(start).Nanoseconds() / int64(iters)
		if hits != 3 {
			t.Fatalf("hits = %d want 3", hits)
		}
		got, _ := s.QueryWarnings(q)
		tiers[ti] = tier{
			n:         n,
			ns:        ns,
			hits:      len(got),
			depth:     s.IndexDepth(),
			indexSize: s.IndexSize(),
		}
	}

	logPath := filepath.Join("..", "testlogs", "perf_two_tiers.log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	w := bufio.NewWriter(lf)
	defer w.Flush()

	fmt.Fprintf(w, "两档对象总数对照（命中条数固定为 %d，预警窗口 10 天）\n", tiers[0].hits)
	fmt.Fprintf(w, "%-8s %-12s %-14s %-10s %-14s\n", "objects", "ns/op", "hits", "treeDepth", "indexSize")
	for _, x := range tiers {
		fmt.Fprintf(w, "%-8d %-12d %-14d %-10d %-14d\n", x.n, x.ns, x.hits, x.depth, x.indexSize)
	}
	ratio := float64(tiers[1].ns) / float64(tiers[0].ns)
	fmt.Fprintf(w, "耗时比(20k/2k) = %.2f；对象数比 = 10.00\n", ratio)

	t.Logf("2k: %dns/op depth=%d | 20k: %dns/op depth=%d | ratio=%.2f",
		tiers[0].ns, tiers[0].depth, tiers[1].ns, tiers[1].depth, ratio)

	if ratio > 3.0 {
		t.Fatalf("warning query scales with object count: ratio %.2f", ratio)
	}
	// treap 高度从 2k 到 20k 只应增加约 log2(10)≈3.3。
	if tiers[1].depth-tiers[0].depth > 8 {
		t.Fatalf("index depth grows too fast: %d -> %d", tiers[0].depth, tiers[1].depth)
	}
}

// TestUsabilityCostLinearAttachments 验证可使用判定开销只与设备附件数相关：
// 系统中其他设备数量扩大不改变判定耗时。
func TestUsabilityCostIsolated(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	measure := func(otherDevices int) int64 {
		s := speq.New()
		mustT(t, s.AddCategory(speq.CategoryConfig{
			Code: "B", Kind: speq.KindDevice, PeriodMonths: 120,
			EarlyWindowDays: 1, MinUnsealDays: 1, WarningLeadDays: 1,
		}))
		mustT(t, s.AddCategory(speq.CategoryConfig{
			Code: "SV", Kind: speq.KindSafetyValve, PeriodMonths: 120,
			EarlyWindowDays: 1, MinUnsealDays: 1, WarningLeadDays: 1,
		}))
		base := 10000
		// 目标设备挂 5 个附件（含一个安全阀）。
		_, err := s.RegisterDevice(base, "TARGET", "B", base)
		mustT(t, err)
		for i := 0; i < 5; i++ {
			vid := fmt.Sprintf("VT%02d", i)
			_, err := s.RegisterAttachment(base, vid, "SV", speq.KindSafetyValve, base)
			mustT(t, err)
			mustT(t, s.MountAttachment(base, vid, "TARGET"))
		}
		// 其余无关设备。
		for i := 0; i < otherDevices; i++ {
			id := fmt.Sprintf("O%07d", i)
			_, err := s.RegisterDevice(base+i, id, "B", base+i)
			mustT(t, err)
		}
		res := testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				r, err := s.CheckUsable(base, "TARGET")
				if err != nil || !r.Usable {
					b.Fatalf("usable=%v err=%v", r, err)
				}
			}
		})
		return res.NsPerOp()
	}
	nsSmall := measure(100)
	nsLarge := measure(10000)
	t.Logf("usability ns/op: 100 others=%d, 10000 others=%d ratio=%.2f",
		nsSmall, nsLarge, float64(nsLarge)/float64(nsSmall))
	if float64(nsLarge)/float64(nsSmall) > 3.0 {
		t.Fatalf("usability cost depends on total objects: %d vs %d", nsSmall, nsLarge)
	}
}

func mustT(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
