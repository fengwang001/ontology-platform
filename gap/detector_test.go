package gap

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"testing"
)

// logState 打印一次摄入后的序号、水位、在途集、洞集与判定依据。
func logState(t *testing.T, r Result, d *Detector, stage string) {
	t.Helper()
	t.Logf("[%s] seq=%d water=%d inflight=%v holes=%v advanced=%v duplicate=%v | %s",
		stage, r.Seq, d.Water(), d.Inflight(), d.Holes(),
		r.Advanced, r.Duplicate, r.Reason)
}

func TestZeroWindowStrict(t *testing.T) {
	// window=0：任何超前到达都意味着当前缺口立即判洞（严格连续语义）。
	d, _ := New(0)

	r, err := d.Ingest(2)
	if err != nil {
		t.Fatal(err)
	}
	logState(t, r, d, "零窗口超前")
	if d.Water() != 2 || len(d.Holes()) != 1 || d.Holes()[0] != 1 {
		t.Fatalf("window=0 时超前到达 2 应立即判洞 1，water=%d holes=%v",
			d.Water(), d.Holes())
	}
	if inflight := d.Inflight(); len(inflight) != 0 {
		t.Fatalf("洞确认后 2 应并入前缀，在途集应为空，实际 %v", inflight)
	}

	r, err = d.Ingest(1)
	if err != nil {
		t.Fatal(err)
	}
	logState(t, r, d, "零窗口补齐")
	if d.Water() != 2 {
		t.Fatalf("补齐后水位应为 2，实际 %d", d.Water())
	}
	if len(d.Holes()) != 1 || d.Holes()[0] != 1 {
		t.Fatalf("洞 1 不可回撤，实际 %v", d.Holes())
	}
	if err := d.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

func TestReorderNotEarlyJudged(t *testing.T) {
	d, err := New(5)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 1,2,3 连续到达，水位推进到 3。
	for _, seq := range []int64{1, 2, 3} {
		r, err := d.Ingest(seq)
		if err != nil {
			t.Fatalf("Ingest(%d): %v", seq, err)
		}
		logState(t, r, d, "连续前缀")
		if d.Water() != seq {
			t.Fatalf("摄入 %d 后水位应为 %d，实际 %d", seq, seq, d.Water())
		}
	}

	// 5 提前到达：距缺口 4 仅 1，未越过窗口，不得判洞。
	r, err := d.Ingest(5)
	if err != nil {
		t.Fatalf("Ingest(5): %v", err)
	}
	logState(t, r, d, "轻微乱序")
	if d.Water() != 3 {
		t.Fatalf("乱序未补齐时水位应保持 3，实际 %d", d.Water())
	}
	if len(d.Holes()) != 0 {
		t.Fatalf("窗口内乱序不应判洞，实际 holes=%v", d.Holes())
	}
	if inflight := d.Inflight(); len(inflight) != 1 || inflight[0] != 5 {
		t.Fatalf("在途集应为 [5]，实际 %v", inflight)
	}

	// 4 迟到，前缀补齐，水位推进到 5，无洞。
	r, err = d.Ingest(4)
	if err != nil {
		t.Fatalf("Ingest(4): %v", err)
	}
	logState(t, r, d, "乱序补齐")
	if d.Water() != 5 {
		t.Fatalf("补齐后水位应为 5，实际 %d", d.Water())
	}
	if len(d.Holes()) != 0 {
		t.Fatalf("乱序补齐后不应有洞，实际 %v", d.Holes())
	}
	if err := d.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

const (
	minInt = -1 << 63
	maxInt = 1<<63 - 1
)

type stateSnapshot struct {
	water    int64
	inflight []int64
	holes    []int64
}

func (d *Detector) snapshot() stateSnapshot {
	return stateSnapshot{water: d.Water(), inflight: d.Inflight(), holes: d.Holes()}
}

func (s stateSnapshot) String() string {
	return fmt.Sprintf("water=%d inflight=%v holes=%v", s.water, s.inflight, s.holes)
}

func (s stateSnapshot) equal(o stateSnapshot) bool {
	return s.water == o.water && equalSlice(s.inflight, o.inflight) &&
		equalSlice(s.holes, o.holes)
}

func equalSlice(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// oracle 是用独立数据结构（map + 每轮重算有序视图）实现的全集预言机，
// 逐条复述规格规则，用于与 Detector 的输出逐点对照。
type oracle struct {
	window   int64
	water    int64
	inflight map[int64]struct{}
	holes    map[int64]struct{}
}

func newOracle(window int64) *oracle {
	return &oracle{
		window:   window,
		inflight: make(map[int64]struct{}),
		holes:    make(map[int64]struct{}),
	}
}

func (o *oracle) ingest(seq int64) {
	if seq <= o.water {
		return
	}
	o.inflight[seq] = struct{}{}

	for {
		next := o.water + 1
		maxInflight, hasMax := int64(0), false
		for s := range o.inflight {
			if !hasMax || s > maxInflight {
				maxInflight, hasMax = s, true
			}
		}
		if !hasMax {
			return
		}
		if _, arrived := o.inflight[next]; arrived {
			delete(o.inflight, next)
			o.water = next
			continue
		}
		if maxInflight <= next+o.window {
			return
		}
		o.holes[next] = struct{}{}
		o.water = next
	}
}

func (o *oracle) inflightSlice() []int64 {
	out := make([]int64, 0, len(o.inflight))
	for s := range o.inflight {
		out = append(out, s)
	}
	sortInts(out)
	return out
}

func (o *oracle) holesSlice() []int64 {
	out := make([]int64, 0, len(o.holes))
	for s := range o.holes {
		out = append(out, s)
	}
	sortInts(out)
	return out
}

func sortInts(a []int64) {
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
}

func TestIllegalInputsAreRejectedAtomically(t *testing.T) {
	d, err := New(1)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 先建立正常状态：water=1，inflight={3}（3 <= 2+1，不判洞）。
	if _, err := d.Ingest(1); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Ingest(3); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		seq  int64
		want error
	}{
		{"零序号", 0, ErrIllegalSequence},
		{"负序号", -7, ErrIllegalSequence},
		{"最小int64", minInt, ErrIllegalSequence},
		{"溢出序号", maxSequence + 1, ErrSequenceOverflow},
		{"最大int64", maxInt, ErrSequenceOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := d.snapshot()
			r, err := d.Ingest(tc.seq)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Ingest(%d) 期望 %v，实际 %v", tc.seq, tc.want, err)
			}
			if r.Seq != 0 || r.Water != 0 || r.Advanced || r.Duplicate ||
				len(r.NewHoles) != 0 || r.Reason != "" {
				t.Fatalf("拒绝摄入必须返回零值 Result，实际 %+v", r)
			}
			after := d.snapshot()
			if !before.equal(after) {
				t.Fatalf("失败摄入改变了状态：before=%+v after=%+v", before, after)
			}
			t.Logf("seq=%d 被拒绝（%v），水位/在途集/洞集不变：%s", tc.seq, err, before)
		})
	}

	if _, err := New(-1); !errors.Is(err, ErrIllegalWindow) {
		t.Fatalf("负窗口应返回 ErrIllegalWindow，实际 %v", err)
	}
	if _, err := New(minInt); !errors.Is(err, ErrIllegalWindow) {
		t.Fatalf("极小窗口应返回 ErrIllegalWindow，实际 %v", err)
	}
}

func TestConcurrentDistinctSequences(t *testing.T) {
	const n = 2000
	// 全集互异序号在最坏调度下首末到达可相差 n-1，窗口取 n-1
	// 保证“全部到达”时任何序号都不可能被越过窗口而误判。
	d, _ := New(n - 1)

	seqs := rand.Perm(n)
	var wg sync.WaitGroup
	for _, v := range seqs {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			if _, err := d.Ingest(int64(seq) + 1); err != nil {
				t.Errorf("Ingest(%d): %v", seq+1, err)
			}
		}(v)
	}

	// 查询与自检与摄入并发执行，不得死锁或观察到被破坏的不变量。
	stop := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(2)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = d.Water()
				_ = d.Inflight()
				_ = d.Holes()
			}
		}
	}()
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if err := d.Check(); err != nil {
					t.Errorf("并发自检失败: %v", err)
					return
				}
			}
		}
	}()

	wg.Wait()
	close(stop)
	readerWG.Wait()

	if d.Water() != n {
		t.Fatalf("全部互异序号摄入后水位应为 %d，实际 %d", n, d.Water())
	}
	if len(d.Holes()) != 0 {
		t.Fatalf("全集到达后洞集必须为空，实际 %v", d.Holes())
	}
	if len(d.Inflight()) != 0 {
		t.Fatalf("全集到达后在途集必须为空，实际 %v", d.Inflight())
	}
	if err := d.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	t.Logf("并发完成：seq 数=%d water=%d inflight=%v holes=%v（互异序号乱序喂入）",
		n, d.Water(), d.Inflight(), d.Holes())
}

// TestFullSetCrossCheck 使用全集对照法核对结果：
// 维护一份独立的“到达全集”，并用与生产代码完全相同的精确规则
// （连续前缀并入；缺口在最远在途序号越过 缺口+window 时判洞且不回撤）
// 逐点模拟出预言机期望状态。每喂入一个序号，检测器的水位、在途集、洞集
// 都必须与预言机逐元素一致；最后再用足够远的驱动序号收敛全部残留缺口。
func TestFullSetCrossCheck(t *testing.T) {
	const (
		smallUniverse = 300
		largeUniverse = 4000
	)
	rng := rand.New(rand.NewPCG(0x1234abcd, 0x5678ef01))

	mustMatch := func(t *testing.T, iter int, stage string, seq int64,
		d *Detector, o *oracle) {
		t.Helper()
		if d.Water() != o.water ||
			!equalSlice(d.Inflight(), o.inflightSlice()) ||
			!equalSlice(d.Holes(), o.holesSlice()) {
			t.Fatalf("iter=%d %s seq=%d：与全集预言机不一致\n"+
				"detector: water=%d inflight=%v holes=%v\n"+
				"oracle  : water=%d inflight=%v holes=%v",
				iter, stage, seq,
				d.Water(), d.Inflight(), d.Holes(),
				o.water, o.inflightSlice(), o.holesSlice())
		}
	}

	// 小规模：多轮、每一步都与预言机逐点对照（覆盖全部中间状态）。
	for iter := 0; iter < 40; iter++ {
		window := int64(rng.IntN(10))
		d, err := New(window)
		if err != nil {
			t.Fatalf("iter=%d New: %v", iter, err)
		}
		oracle := newOracle(window)

		arrived := make(map[int64]bool)
		for seq := int64(1); seq <= smallUniverse; seq++ {
			if rng.IntN(2) == 0 {
				arrived[seq] = true
			}
		}
		ordered := make([]int64, 0, len(arrived))
		for seq := range arrived {
			ordered = append(ordered, seq)
		}
		rng.Shuffle(len(ordered), func(i, j int) {
			ordered[i], ordered[j] = ordered[j], ordered[i]
		})

		for step, seq := range ordered {
			if _, err := d.Ingest(seq); err != nil {
				t.Fatalf("iter=%d Ingest(%d): %v", iter, seq, err)
			}
			oracle.ingest(seq)
			mustMatch(t, iter, fmt.Sprintf("step=%d", step), seq, d, oracle)
		}

		sentinel := smallUniverse + window + 1
		if _, err := d.Ingest(sentinel); err != nil {
			t.Fatalf("iter=%d Ingest(%d): %v", iter, sentinel, err)
		}
		oracle.ingest(sentinel)
		mustMatch(t, iter, "收敛", sentinel, d, oracle)

		if err := d.Check(); err != nil {
			t.Fatalf("iter=%d 自检失败: %v", iter, err)
		}
		t.Logf("iter=%d window=%d 到达=%d/%d water=%d inflight=%d holes=%d（与全集预言机一致）",
			iter, window, len(arrived), smallUniverse,
			d.Water(), len(d.Inflight()), len(d.Holes()))
	}

	// 大规模：仅在收敛后做一次全集逐元素对照，验证量级放大后结果仍正确。
	window := int64(5)
	d, err := New(window)
	if err != nil {
		t.Fatalf("large New: %v", err)
	}
	oracle := newOracle(window)
	arrived := make(map[int64]bool)
	for seq := int64(1); seq <= largeUniverse; seq++ {
		if rng.IntN(2) == 0 {
			arrived[seq] = true
		}
	}
	ordered := make([]int64, 0, len(arrived))
	for seq := range arrived {
		ordered = append(ordered, seq)
	}
	rng.Shuffle(len(ordered), func(i, j int) {
		ordered[i], ordered[j] = ordered[j], ordered[i]
	})
	for _, seq := range ordered {
		if _, err := d.Ingest(seq); err != nil {
			t.Fatalf("large Ingest(%d): %v", seq, err)
		}
		oracle.ingest(seq)
	}
	sentinel := largeUniverse + window + 1
	if _, err := d.Ingest(sentinel); err != nil {
		t.Fatalf("large Ingest(%d): %v", sentinel, err)
	}
	oracle.ingest(sentinel)
	mustMatch(t, 40, "大规模收敛", sentinel, d, oracle)
	if err := d.Check(); err != nil {
		t.Fatalf("large 自检失败: %v", err)
	}
	t.Logf("大规模 window=%d 到达=%d/%d water=%d holes=%d（与全集预言机一致）",
		window, len(arrived), largeUniverse, d.Water(), len(d.Holes()))
}

func TestWindowBoundary(t *testing.T) {
	// window=2：maxInflight == 缺口+2 时必须等待，等于缺口+3 时才判洞。
	d, _ := New(2)

	for _, seq := range []int64{1, 4} {
		r, err := d.Ingest(seq)
		if err != nil {
			t.Fatalf("Ingest(%d): %v", seq, err)
		}
		logState(t, r, d, "边界-等待侧")
	}
	if d.Water() != 1 || len(d.Holes()) != 0 {
		t.Fatalf("maxInflight=4 == 缺口2+window2，不应判洞；water=%d holes=%v",
			d.Water(), d.Holes())
	}

	// 摄入 5：maxInflight=5 > 2+2，序号 2 必须判洞；随后缺口 3 仍满足
	// 5 <= 3+2，停止判洞。
	r, err := d.Ingest(5)
	if err != nil {
		t.Fatalf("Ingest(5): %v", err)
	}
	logState(t, r, d, "边界-判洞侧")
	if d.Water() != 2 {
		t.Fatalf("判洞后水位应为 2，实际 %d", d.Water())
	}
	if holes := d.Holes(); len(holes) != 1 || holes[0] != 2 {
		t.Fatalf("洞集应为 [2]，实际 %v", holes)
	}
	if got := len(r.NewHoles); got != 1 || r.NewHoles[0] != 2 {
		t.Fatalf("本次应仅新判洞 [2]，实际 %v", r.NewHoles)
	}
	if err := d.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

func TestHoleSizeJudgement(t *testing.T) {
	// window=3，连续缺失 6 个序号（4..9）后 10 到达：
	// maxInflight=10 使缺口 k 满足 10 > k+3，即 k < 7，故判洞 4,5,6 共 3 个。
	d, _ := New(3)
	for _, seq := range []int64{1, 2, 3, 10} {
		r, err := d.Ingest(seq)
		if err != nil {
			t.Fatalf("Ingest(%d): %v", seq, err)
		}
		logState(t, r, d, "洞大小")
	}

	if holes := d.Holes(); len(holes) != 3 || holes[0] != 4 || holes[1] != 5 || holes[2] != 6 {
		t.Fatalf("洞集应为 [4 5 6]，实际 %v", holes)
	}
	if d.Water() != 6 {
		t.Fatalf("判洞后水位应为 6，实际 %d", d.Water())
	}
	if inflight := d.Inflight(); len(inflight) != 1 || inflight[0] != 10 {
		t.Fatalf("在途集应为 [10]，实际 %v", inflight)
	}

	// 迟到的洞序号 4 到达：不高于水位，按重复段忽略，洞不回撤。
	r, err := d.Ingest(4)
	if err != nil {
		t.Fatalf("Ingest(4): %v", err)
	}
	logState(t, r, d, "洞序号迟到")
	if !r.Duplicate {
		t.Fatalf("已判洞序号 4 应按重复段忽略")
	}
	if holes := d.Holes(); len(holes) != 3 || holes[0] != 4 || holes[1] != 5 || holes[2] != 6 {
		t.Fatalf("洞集不可回撤，实际 %v", holes)
	}
	if err := d.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

func TestDuplicateSegmentNoFalsePositive(t *testing.T) {
	d, _ := New(2)

	// 先制造一个确认洞 2（1 到达，4 到达，5 > 2+2）。
	for _, seq := range []int64{1, 4, 5} {
		if _, err := d.Ingest(seq); err != nil {
			t.Fatalf("Ingest(%d): %v", seq, err)
		}
	}

	// 重复喂入已确认前缀中的整段序号，任意次数、任意顺序。
	dupes := []int64{1, 1, 2, 1, 2, 2, 1, 2, 2, 1}
	for _, seq := range dupes {
		r, err := d.Ingest(seq)
		if err != nil {
			t.Fatalf("Ingest(%d): %v", seq, err)
		}
		logState(t, r, d, "重复段")
		if !r.Duplicate {
			t.Fatalf("seq=%d 不高于水位，必须标记 duplicate 且不产生任何判定", seq)
		}
		if len(r.NewHoles) != 0 {
			t.Fatalf("重复段摄入不得新判洞，实际 %v", r.NewHoles)
		}
	}

	if d.Water() != 2 {
		t.Fatalf("重复段不得改变水位，实际 %d", d.Water())
	}
	if holes := d.Holes(); len(holes) != 1 || holes[0] != 2 {
		t.Fatalf("洞集必须保持 [2]，实际 %v", holes)
	}
	if inflight := d.Inflight(); len(inflight) != 2 || inflight[0] != 4 || inflight[1] != 5 {
		t.Fatalf("在途集必须保持 [4 5]，实际 %v", inflight)
	}
	if err := d.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}
