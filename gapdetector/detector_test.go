package gapdetector

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func kindName(kind DecisionKind) string {
	switch kind {
	case DecisionIgnored:
		return "忽略"
	case DecisionMerged:
		return "并入"
	case DecisionBuffered:
		return "在途"
	}
	return "未知"
}

// logState 按用例要求打印序号、水位、在途集、洞集与判定依据。
func logState(t *testing.T, d *Detector, dec Decision) {
	t.Helper()
	t.Logf("seq=%d 判定=%s 水位=%d 在途集=%v 洞集=%v 新判洞=%v 依据=%s",
		dec.Seq, kindName(dec.Kind), d.Watermark(), d.Inflight(), d.Holes(),
		dec.HolesDeclared, dec.Reason)
}

func mustNew(t *testing.T, window uint64) *Detector {
	t.Helper()
	d, err := New(window)
	if err != nil {
		t.Fatalf("New(window=%d) 失败: %v", window, err)
	}
	return d
}

func mustIngest(t *testing.T, d *Detector, seq uint64) Decision {
	t.Helper()
	dec, err := d.Ingest(seq)
	if err != nil {
		t.Fatalf("Ingest(seq=%d) 失败: %v", seq, err)
	}
	return dec
}

// TestOutOfOrderNotPremature 验证窗口内的乱序不会被提前判洞，
// 只有在途最大值越过 水位+1+窗口 时才判洞，且判洞后迟到序号被忽略。
func TestOutOfOrderNotPremature(t *testing.T) {
	d := mustNew(t, 3)

	for _, seq := range []uint64{1, 2, 4, 5} {
		logState(t, d, mustIngest(t, d, seq))
	}
	if got := d.Holes(); len(got) != 0 {
		t.Fatalf("窗口内乱序被误判为洞: %v", got)
	}
	if got := d.Watermark(); got != 2 {
		t.Fatalf("水位应为 2，实际 %d", got)
	}

	// 7 使在途最大值 7 越过 3+3=6，3 被判为洞；4、5 顺势并入。
	logState(t, d, mustIngest(t, d, 7))
	if got, want := d.Holes(), []uint64{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("洞集应为 %v，实际 %v", want, got)
	}
	if got := d.Watermark(); got != 5 {
		t.Fatalf("水位应为 5，实际 %d", got)
	}

	// 迟到的 3 已被判洞，不得回撤，按重复/迟到忽略。
	dec := mustIngest(t, d, 3)
	logState(t, d, dec)
	if dec.Kind != DecisionIgnored {
		t.Fatalf("迟到的已判洞序号应被忽略，实际判定 %v", kindName(dec.Kind))
	}
	if got, want := d.Holes(), []uint64{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("判洞不得回撤，洞集应为 %v，实际 %v", want, got)
	}
	if err := d.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestDuplicateSegmentNoFalsePositive 验证重复段与重复序号全部被忽略，
// 不产生任何误报。
func TestDuplicateSegmentNoFalsePositive(t *testing.T) {
	d := mustNew(t, 2)

	for seq := uint64(1); seq <= 5; seq++ {
		logState(t, d, mustIngest(t, d, seq))
	}
	if got := d.Watermark(); got != 5 {
		t.Fatalf("水位应为 5，实际 %d", got)
	}

	// 整段重放 1..5，再加单个重复 3，全部应被忽略。
	for _, seq := range []uint64{1, 2, 3, 4, 5, 3, 3} {
		dec := mustIngest(t, d, seq)
		logState(t, d, dec)
		if dec.Kind != DecisionIgnored {
			t.Fatalf("重复序号 %d 应被忽略，实际判定 %v", seq, kindName(dec.Kind))
		}
	}
	if got := d.Watermark(); got != 5 {
		t.Fatalf("重复段不得改变水位，应为 5，实际 %d", got)
	}
	if got := d.Holes(); len(got) != 0 {
		t.Fatalf("重复段不得产生洞，实际洞集 %v", got)
	}
	if got := d.Inflight(); len(got) != 0 {
		t.Fatalf("重复段不得产生在途，实际在途集 %v", got)
	}
	if err := d.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestGapSizeDetermination 验证连续多个洞的大小判定：窗口为 2 时，
// 水位 1 之后直接到达 10，应判出 2..7 共 6 个洞，10 留在在途集。
func TestGapSizeDetermination(t *testing.T) {
	d := mustNew(t, 2)

	logState(t, d, mustIngest(t, d, 1))
	dec := mustIngest(t, d, 10)
	logState(t, d, dec)

	wantHoles := []uint64{2, 3, 4, 5, 6, 7}
	if got := d.Holes(); !reflect.DeepEqual(got, wantHoles) {
		t.Fatalf("洞集应为 %v（大小 %d），实际 %v（大小 %d）",
			wantHoles, len(wantHoles), got, len(got))
	}
	if got, want := d.Inflight(), []uint64{10}; !reflect.DeepEqual(got, want) {
		t.Fatalf("在途集应为 %v，实际 %v", want, got)
	}
	if got := d.Watermark(); got != 7 {
		t.Fatalf("水位应为 7，实际 %d", got)
	}

	// 补上 8、9 后，连续前缀并入到 10，洞集保持不变。
	logState(t, d, mustIngest(t, d, 8))
	logState(t, d, mustIngest(t, d, 9))
	if got := d.Watermark(); got != 10 {
		t.Fatalf("水位应为 10，实际 %d", got)
	}
	if got := d.Holes(); !reflect.DeepEqual(got, wantHoles) {
		t.Fatalf("洞集应保持 %v，实际 %v", wantHoles, got)
	}
	if err := d.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestInvalidInputsRejected 验证非法序号、非法窗口与序号溢出被整体拒绝，
// 原因可区分，且失败不改变水位、在途集与洞集。
func TestInvalidInputsRejected(t *testing.T) {
	if _, err := New(MaxWindow + 1); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("非法窗口应返回 ErrInvalidWindow，实际 %v", err)
	}

	d := mustNew(t, 3)
	logState(t, d, mustIngest(t, d, 1))
	logState(t, d, mustIngest(t, d, 5))
	wmBefore, inflightBefore, holesBefore := d.Watermark(), d.Inflight(), d.Holes()

	cases := []struct {
		name string
		seq  uint64
		want error
	}{
		{"序号为零", 0, ErrInvalidSeq},
		{"序号越上界", MaxSeq + 1, ErrInvalidSeq},
		{"序号加窗口溢出", MaxSeq - 2, ErrOverflow},
	}
	for _, tc := range cases {
		_, err := d.Ingest(tc.seq)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: 应返回 %v，实际 %v", tc.name, tc.want, err)
		}
		t.Logf("%s: seq=%d 被拒绝，原因=%v", tc.name, tc.seq, err)
	}

	if got := d.Watermark(); got != wmBefore {
		t.Fatalf("失败不得改变水位: 前 %d 后 %d", wmBefore, got)
	}
	if got := d.Inflight(); !reflect.DeepEqual(got, inflightBefore) {
		t.Fatalf("失败不得改变在途集: 前 %v 后 %v", inflightBefore, got)
	}
	if got := d.Holes(); !reflect.DeepEqual(got, holesBefore) {
		t.Fatalf("失败不得改变洞集: 前 %v 后 %v", holesBefore, got)
	}
	if err := d.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestConcurrentDistinctIngest 验证并发喂入互不相同的序号后水位正确、
// 洞集为空，且并发读取到的水位单调不减。
//
// 判洞一旦做出不回撤，而 goroutine 调度顺序任意，因此窗口取不小于序号
// 总数，使任意调度下在途最大值与水位的差都不越窗，洞集必然为空。
func TestConcurrentDistinctIngest(t *testing.T) {
	const n = 2000
	d := mustNew(t, n)

	seqs := rand.Perm(n)
	var wg sync.WaitGroup

	// 读取方：并发查询与自检，记录水位序列，断言单调不减。
	stop := make(chan struct{})
	var readerWg sync.WaitGroup
	wmSamples := make([][]uint64, 4)
	for r := range wmSamples {
		readerWg.Add(1)
		go func(idx int) {
			defer readerWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				wmSamples[idx] = append(wmSamples[idx], d.Watermark())
				_ = d.Inflight()
				_ = d.Holes()
				if err := d.SelfCheck(); err != nil {
					t.Errorf("并发自检失败: %v", err)
					return
				}
			}
		}(r)
	}

	// 写入方：并发喂入 1..n 的乱序排列，互不重复。
	for _, s := range seqs {
		wg.Add(1)
		go func(seq uint64) {
			defer wg.Done()
			if _, err := d.Ingest(seq); err != nil {
				t.Errorf("Ingest(seq=%d) 失败: %v", seq, err)
			}
		}(uint64(s) + 1)
	}
	wg.Wait()
	close(stop)
	readerWg.Wait()

	for idx, samples := range wmSamples {
		for i := 1; i < len(samples); i++ {
			if samples[i] < samples[i-1] {
				t.Fatalf("读取方 %d 观测到水位回退: %v...", idx, samples[i-1:i+1])
			}
		}
	}
	if got := d.Watermark(); got != n {
		t.Fatalf("并发喂入全集后水位应为 %d，实际 %d", n, got)
	}
	if got := d.Holes(); len(got) != 0 {
		t.Fatalf("并发喂入全集后洞集应为空，实际 %v", got)
	}
	if got := d.Inflight(); len(got) != 0 {
		t.Fatalf("并发喂入全集后在途集应为空，实际 %v", got)
	}
	t.Logf("并发喂入 %d 个互异序号完成: 水位=%d 在途集=%v 洞集=%v", n, d.Watermark(), d.Inflight(), d.Holes())
	if err := d.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestFullSetCrossCheck 用全集对照核对结果：构造 1..n 全集，随机打乱并
// 随机扣掉一个子集作为真实丢失，喂入其余序号后，检测器报出的洞集必须与
// 被扣掉的集合完全一致，且 水位+在途+洞 能无重叠地重建全集。
//
// 打乱采用窗口内有界乱序（按 window+1 分块、块内洗牌），保证任意序号的
// 位移不超过窗口，迟到的真实存在序号不会被误判为洞；被扣序号限制在
// [1, n-window-1]，保证其上方必有已到达序号越过窗口触发判洞。
func TestFullSetCrossCheck(t *testing.T) {
	const (
		n      = 500
		window = 6
	)
	rng := rand.New(rand.NewSource(42))
	d := mustNew(t, window)

	full := make([]uint64, 0, n)
	for seq := uint64(1); seq <= n; seq++ {
		full = append(full, seq)
	}
	missing := make(map[uint64]struct{})
	for _, seq := range full {
		if seq+window < n && rng.Intn(20) == 0 {
			missing[seq] = struct{}{}
		}
	}

	// 窗口内有界乱序：按序号值每 window+1 个分一组，组内洗牌后按组喂入，
	// 使每个序号的位移不超过 window（按值分组，不受缺失序号压缩位置影响）。
	const chunk = window + 1
	feed := make([]uint64, 0, n)
	for start := uint64(1); start <= n; start += chunk {
		block := make([]uint64, 0, chunk)
		for seq := start; seq < start+chunk && seq <= n; seq++ {
			if _, lost := missing[seq]; !lost {
				block = append(block, seq)
			}
		}
		rng.Shuffle(len(block), func(i, j int) { block[i], block[j] = block[j], block[i] })
		feed = append(feed, block...)
	}
	for _, seq := range feed {
		mustIngest(t, d, seq)
	}

	gotHoles := d.Holes()
	wantHoles := make([]uint64, 0, len(missing))
	for seq := range missing {
		wantHoles = append(wantHoles, seq)
	}
	sort.Slice(wantHoles, func(i, j int) bool { return wantHoles[i] < wantHoles[j] })
	if !reflect.DeepEqual(gotHoles, wantHoles) {
		t.Fatalf("洞集与全集对照不符:\n期望 %v\n实际 %v", wantHoles, gotHoles)
	}

	// 全集对照重建：洞集是水位内确认丢失的子集，因此
	// ([1..水位] \ 洞集) ∪ 在途集 必须恰好等于实际喂入集合（全集 \ 丢失集），
	// 且 洞集 == 丢失集，两者合并即无重叠地重建全集 1..n。
	arrived := make(map[uint64]struct{}, n)
	for seq := uint64(1); seq <= d.Watermark(); seq++ {
		if _, lost := missing[seq]; !lost {
			arrived[seq] = struct{}{}
		}
	}
	for _, seq := range d.Inflight() {
		arrived[seq] = struct{}{}
	}
	for seq := uint64(1); seq <= n; seq++ {
		_, gotArrived := arrived[seq]
		_, wantMissing := missing[seq]
		if gotArrived == wantMissing {
			t.Fatalf("全集重建失败: 序号 %d 到达=%v 丢失=%v（水位=%d 在途=%v 洞=%v）",
				seq, gotArrived, wantMissing, d.Watermark(), d.Inflight(), gotHoles)
		}
	}
	t.Logf("全集对照通过: n=%d 丢失=%d 水位=%d 在途=%v 洞集=%v",
		n, len(missing), d.Watermark(), d.Inflight(), gotHoles)
	if err := d.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// TestSelfCheckDetectsCorruption 直接破坏内部状态，验证自检能报出可区分的失败。
func TestSelfCheckDetectsCorruption(t *testing.T) {
	d := mustNew(t, 2)
	for _, seq := range []uint64{1, 2, 4} {
		mustIngest(t, d, seq)
	}
	if err := d.SelfCheck(); err != nil {
		t.Fatalf("正常状态自检应通过: %v", err)
	}

	d.mu.Lock()
	d.inflight[1] = struct{}{}
	d.mu.Unlock()
	err := d.SelfCheck()
	if err == nil {
		t.Fatal("在途集混入不高于水位的序号，自检应失败")
	}
	t.Logf("自检按预期报出不变量破坏: %v", err)
}

// TestWindowBoundary 验证窗口边界：在途最大值恰等于 候选序号+窗口 时不判洞，
// 再超过 1 才判洞。
func TestWindowBoundary(t *testing.T) {
	d := mustNew(t, 3)

	logState(t, d, mustIngest(t, d, 1))
	// 候选序号为 2，5 == 2+3 恰在边界上，不判洞。
	logState(t, d, mustIngest(t, d, 5))
	if got := d.Holes(); len(got) != 0 {
		t.Fatalf("在途最大值等于候选序号+窗口不应判洞，实际洞集 %v", got)
	}
	// 6 > 2+3，越过边界，判 2 为洞；判后候选 3 满足 6 <= 3+3，停止收敛。
	logState(t, d, mustIngest(t, d, 6))
	if got, want := d.Holes(), []uint64{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("洞集应为 %v，实际 %v", want, got)
	}
	if got, want := d.Inflight(), []uint64{5, 6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("在途集应为 %v，实际 %v", want, got)
	}
	// 补上 3、4 后连续前缀整体并入。
	logState(t, d, mustIngest(t, d, 3))
	logState(t, d, mustIngest(t, d, 4))
	if got := d.Watermark(); got != 6 {
		t.Fatalf("水位应为 6，实际 %d", got)
	}
	if got := d.Inflight(); len(got) != 0 {
		t.Fatalf("在途集应为空，实际 %v", got)
	}
	if err := d.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}
