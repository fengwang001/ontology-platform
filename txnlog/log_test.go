package txnlog

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func wantErr(t *testing.T, got error, want error, ctx string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: want error %v, got %v", ctx, want, got)
	}
}

func payloads(recs []Record) string {
	var b strings.Builder
	for _, r := range recs {
		b.Write(r.Payload)
	}
	return b.String()
}

// 同一生产者连续多个事务：先提交、后中止、再提交。
func TestCommitThenAbortSameProducer(t *testing.T) {
	l := New()
	if err := l.RegisterProducer("p"); err != nil {
		t.Fatal(err)
	}

	d1, _ := l.AppendData("p", []byte("a"))
	d2, _ := l.AppendData("p", []byte("b"))
	m1, _ := l.AppendMarker("p", true) // 事务1提交
	d3, _ := l.AppendData("p", []byte("c"))
	m2, _ := l.AppendMarker("p", false) // 事务2中止
	d4, _ := l.AppendData("p", []byte("d"))
	m3, _ := l.AppendMarker("p", true) // 事务3提交

	if !(d1 < d2 && d2 < m1 && m1 < d3 && d3 < m2 && m2 < d4 && d4 < m3) {
		t.Fatalf("位点不连续递增: %d %d %d %d %d %d %d", d1, d2, m1, d3, m2, d4, m3)
	}

	if err := l.AdvanceHighWatermark(m3 + 1); err != nil {
		t.Fatal(err)
	}
	if got := l.StableOffset(); got != m3+1 {
		t.Fatalf("全部有结论后稳定位点应为日志末尾 %d, got %d", m3+1, got)
	}

	recs, err := l.ReadCommitted(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := payloads(recs); got != "abd" {
		t.Fatalf("已提交读应只返回事务1与事务3的数据, got %q", got)
	}
	for _, r := range recs {
		if r.Kind != KindData {
			t.Fatalf("已提交读不得返回控制标记: %+v", r)
		}
	}

	// 从中间位点读取，结果可复现。
	first, err := l.ReadCommitted(d2, 100)
	if err != nil {
		t.Fatal(err)
	}
	second, err := l.ReadCommitted(d2, 100)
	if err != nil || payloads(first) != payloads(second) || payloads(first) != "bd" {
		t.Fatalf("重复读取结果不一致: %q vs %q, err=%v", payloads(first), payloads(second), err)
	}
}

// 稳定位点被未决事务卡住，结论可见后才推进。
func TestStableOffsetAdvance(t *testing.T) {
	l := New()
	l.RegisterProducer("a")
	l.RegisterProducer("b")

	a1, _ := l.AppendData("a", []byte("a1"))
	l.AppendData("b", []byte("b1"))
	l.AppendMarker("b", true) // b 已提交
	l.AppendData("a", []byte("a2"))

	// HW 越过 a2 但 a 的结论尚不可见：稳定位点被卡在 a1。
	if err := l.AdvanceHighWatermark(4); err != nil {
		t.Fatal(err)
	}
	if got := l.StableOffset(); got != a1 {
		t.Fatalf("未决事务 a 应卡住稳定位点于 %d, got %d", a1, got)
	}
	recs, _ := l.ReadCommitted(0, 100)
	if got := payloads(recs); got != "" {
		t.Fatalf("稳定位点之前无已提交数据, got %q", got)
	}

	// 提交标记位于日志末尾、HW 未覆盖时结论不可见，稳定位点仍卡在 a1。
	mA, _ := l.AppendMarker("a", true)
	if got := l.StableOffset(); got != a1 {
		t.Fatalf("标记尚未可见时稳定位点应仍为 %d, got %d", a1, got)
	}

	// HW 越过标记后，两个事务结论均可见，稳定位点追平 HW。
	if err := l.AdvanceHighWatermark(mA + 1); err != nil {
		t.Fatal(err)
	}
	if got := l.StableOffset(); got != mA+1 {
		t.Fatalf("稳定位点应追平高水位 %d, got %d", mA+1, got)
	}
	recs, _ = l.ReadCommitted(0, 100)
	if got := payloads(recs); got != "a1b1a2" {
		t.Fatalf("按位点顺序应为 a1,b1,a2, got %q", got)
	}

	// 稳定位点只进不退：回退高水位被拒且无副作用。
	wantErr(t, l.AdvanceHighWatermark(0), ErrInvalidHighWatermark, "高水位回退")
	if got := l.HighWatermark(); got != mA+1 {
		t.Fatalf("拒绝后高水位被改动: %d", got)
	}
	if got := l.StableOffset(); got != mA+1 {
		t.Fatalf("拒绝后稳定位点被改动: %d", got)
	}
}

func snapshot(t *testing.T, l *Log) string {
	t.Helper()
	recs, err := l.ReadCommitted(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("hw=%d stable=%d read=%q", l.HighWatermark(), l.StableOffset(), payloads(recs))
}

// 各类非法输入互不相同、可区分，且拒绝不留痕。
func TestInvalidInputsLeaveNoTrace(t *testing.T) {
	l := New()
	l.RegisterProducer("p")
	l.AppendData("p", []byte("x"))
	l.AppendMarker("p", true)
	l.AdvanceHighWatermark(2)
	before := snapshot(t, l)

	wantErr(t, l.RegisterProducer(""), ErrUnknownProducer, "注册空生产者")
	_, err := l.AppendData("", []byte("y"))
	wantErr(t, err, ErrUnknownProducer, "空生产者写数据")
	_, err = l.AppendData("ghost", []byte("y"))
	wantErr(t, err, ErrUnknownProducer, "未注册生产者写数据")
	_, err = l.AppendMarker("", true)
	wantErr(t, err, ErrUnknownProducer, "空生产者写标记")
	_, err = l.AppendMarker("ghost", false)
	wantErr(t, err, ErrUnknownProducer, "未注册生产者写标记")
	_, err = l.AppendMarker("p", true)
	wantErr(t, err, ErrNoOngoingTransaction, "无进行中事务却写标记")
	wantErr(t, l.AdvanceHighWatermark(-1), ErrInvalidHighWatermark, "负高水位")
	wantErr(t, l.AdvanceHighWatermark(1), ErrInvalidHighWatermark, "高水位倒退")
	wantErr(t, l.AdvanceHighWatermark(3), ErrInvalidHighWatermark, "高水位越过末尾")
	_, err = l.ReadCommitted(-1, 10)
	wantErr(t, err, ErrInvalidReadStart, "负读取起点")
	_, err = l.ReadCommitted(l.StableOffset()+1, 10)
	wantErr(t, err, ErrInvalidReadStart, "读取起点越过稳定位点")

	if after := snapshot(t, l); after != before {
		t.Fatalf("拒绝后状态发生改变:\nbefore %s\nafter  %s", before, after)
	}

	// 错误原因两两可区分。
	errs := []error{ErrUnknownProducer, ErrNoOngoingTransaction, ErrInvalidHighWatermark, ErrInvalidReadStart}
	for i, a := range errs {
		for j, b := range errs {
			if i != j && (errors.Is(a, b) || a.Error() == b.Error()) {
				t.Fatalf("错误类别不可区分: %v vs %v", a, b)
			}
		}
	}

	// 拒绝后系统仍可正常工作。
	if _, err := l.AppendData("p", []byte("z")); err != nil {
		t.Fatalf("拒绝后正常追加失败: %v", err)
	}
}

// refRecord 批量参照模型中的记录。
type refRecord struct {
	producer string
	kind     RecordKind
	payload  string
}

type refDecision struct {
	first, end int64
	committed  bool
}

// refModel 用独立朴素写法实现同一套规则，作为批量参照。
type refModel struct {
	records []refRecord
	hw      int64
	open    map[string]int64
	decided map[string][]refDecision
}

func newRefModel() *refModel {
	return &refModel{open: map[string]int64{}, decided: map[string][]refDecision{}}
}

func (m *refModel) appendData(producer, payload string) int64 {
	off := int64(len(m.records))
	if _, ok := m.open[producer]; !ok {
		m.open[producer] = off
	}
	m.records = append(m.records, refRecord{producer, KindData, payload})
	return off
}

func (m *refModel) appendMarker(producer string, commit bool) int64 {
	off := int64(len(m.records))
	m.decided[producer] = append(m.decided[producer], refDecision{m.open[producer], off, commit})
	delete(m.open, producer)
	kind := KindCommit
	if !commit {
		kind = KindAbort
	}
	m.records = append(m.records, refRecord{producer, kind, ""})
	return off
}

func (m *refModel) stable() int64 {
	s := m.hw
	for _, first := range m.open {
		if first < s {
			s = first
		}
	}
	for _, ds := range m.decided {
		last := ds[len(ds)-1]
		if last.end >= m.hw && last.first < s {
			s = last.first
		}
	}
	return s
}

func (m *refModel) read(from int64) string {
	var b strings.Builder
	stable := m.stable()
	for i := from; i < stable; i++ {
		r := m.records[i]
		if r.kind != KindData {
			continue
		}
		committed := false
		for _, d := range m.decided[r.producer] {
			if d.first <= i && i < d.end {
				committed = d.committed
			}
		}
		if committed {
			b.WriteString(r.payload)
		}
	}
	return b.String()
}

// 随机操作流与批量参照逐步对比，打印每步输入、稳定位点与判定依据。
func TestMatchesBatchReference(t *testing.T) {
	l := New()
	ref := newRefModel()
	rng := rand.New(rand.NewSource(310))
	producers := []string{"p0", "p1", "p2"}
	for _, p := range producers {
		l.RegisterProducer(p)
	}

	check := func(step int) {
		t.Helper()
		if got, want := l.StableOffset(), ref.stable(); got != want {
			t.Fatalf("step %d: 稳定位点分歧 got=%d want=%d", step, got, want)
		}
		recs, err := l.ReadCommitted(0, 1<<30)
		if err != nil {
			t.Fatalf("step %d: 读取失败: %v", step, err)
		}
		if got, want := payloads(recs), ref.read(0); got != want {
			t.Fatalf("step %d: 已提交读分歧\ngot  %q\nwant %q", step, got, want)
		}
	}

	for step := 0; step < 400; step++ {
		p := producers[rng.Intn(len(producers))]
		switch rng.Intn(6) {
		case 0, 1: // 写数据
			payload := fmt.Sprintf("%s#%d;", p, step)
			off, err := l.AppendData(p, []byte(payload))
			if err != nil {
				t.Fatalf("step %d: append 失败: %v", step, err)
			}
			if refOff := ref.appendData(p, payload); off != refOff {
				t.Fatalf("step %d: 位点分歧 %d vs %d", step, off, refOff)
			}
			t.Logf("step %d: AppendData(%s,%q) -> offset=%d; stable=%d; 依据: 新数据位点>=高水位, 不改变稳定位点",
				step, p, payload, off, l.StableOffset())
		case 2: // 写标记
			_, hasOpen := ref.open[p]
			commit := rng.Intn(2) == 0
			off, err := l.AppendMarker(p, commit)
			if !hasOpen {
				wantErr(t, err, ErrNoOngoingTransaction, fmt.Sprintf("step %d", step))
				t.Logf("step %d: AppendMarker(%s,commit=%v) 拒绝 ErrNoOngoingTransaction; stable=%d 不变",
					step, p, commit, l.StableOffset())
				continue
			}
			if err != nil {
				t.Fatalf("step %d: marker 失败: %v", step, err)
			}
			if refOff := ref.appendMarker(p, commit); off != refOff {
				t.Fatalf("step %d: 标记位点分歧 %d vs %d", step, off, refOff)
			}
			t.Logf("step %d: AppendMarker(%s,commit=%v) -> offset=%d; stable=%d; 依据: 标记位点>=高水位时结论不可见仍卡住",
				step, p, commit, off, l.StableOffset())
		case 3, 4: // 推进高水位
			var hw int64
			switch rng.Intn(3) {
			case 0:
				hw = l.HighWatermark() - 1 // 故意回退
			case 1:
				hw = int64(len(ref.records)) + 1 // 故意越过末尾
			default:
				hw = l.HighWatermark() + rng.Int63n(int64(len(ref.records))-l.HighWatermark()+1)
			}
			err := l.AdvanceHighWatermark(hw)
			if hw < ref.hw || hw > int64(len(ref.records)) {
				wantErr(t, err, ErrInvalidHighWatermark, fmt.Sprintf("step %d", step))
				t.Logf("step %d: AdvanceHighWatermark(%d) 拒绝 ErrInvalidHighWatermark; stable=%d 不变",
					step, hw, l.StableOffset())
				continue
			}
			if err != nil {
				t.Fatalf("step %d: 推进失败: %v", step, err)
			}
			ref.hw = hw
			t.Logf("step %d: AdvanceHighWatermark(%d) -> ok; stable=%d; 依据: min(高水位, 未决事务首位点)",
				step, hw, l.StableOffset())
		default: // 读取
			from := rng.Int63n(l.StableOffset() + 1)
			recs, err := l.ReadCommitted(from, 1<<30)
			if err != nil {
				t.Fatalf("step %d: 读取失败: %v", step, err)
			}
			if got, want := payloads(recs), ref.read(from); got != want {
				t.Fatalf("step %d: 从 %d 读取分歧\ngot  %q\nwant %q", step, from, got, want)
			}
			t.Logf("step %d: ReadCommitted(from=%d) -> %d 条; stable=%d; 依据: 稳定位点前且事务已提交",
				step, from, len(recs), l.StableOffset())
		}
		check(step)
	}
}

// 多执行体并发追加、推进高水位与读取：稳定位点只进不退，
// 读取绝不暴露控制标记、未决或中止事务的数据。
func TestConcurrentAccess(t *testing.T) {
	l := New()
	const committers = 4
	const aborters = 2
	const perProducer = 200

	var wg sync.WaitGroup

	// 提交型生产者：每组数据写完后提交。
	for c := 0; c < committers; c++ {
		id := fmt.Sprintf("committer-%d", c)
		l.RegisterProducer(id)
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				if _, err := l.AppendData(id, []byte(fmt.Sprintf("%s/%d;", id, i))); err != nil {
					t.Errorf("append: %v", err)
					return
				}
				if _, err := l.AppendMarker(id, true); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
			}
		}(id)
	}

	// 中止型生产者：数据全部中止，绝不应被读到。
	for c := 0; c < aborters; c++ {
		id := fmt.Sprintf("aborter-%d", c)
		l.RegisterProducer(id)
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				if _, err := l.AppendData(id, []byte(fmt.Sprintf("%s/%d;", id, i))); err != nil {
					t.Errorf("append: %v", err)
					return
				}
				if _, err := l.AppendMarker(id, false); err != nil {
					t.Errorf("abort: %v", err)
					return
				}
			}
		}(id)
	}

	// 高水位推进者：单调推进到日志末尾。
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			hw := l.HighWatermark()
			_ = l.AdvanceHighWatermark(hw + 1) // 越过末尾被拒属正常竞争
		}
	}()

	// 稳定位点观察者：校验只进不退。
	wg.Add(1)
	go func() {
		defer wg.Done()
		prev := int64(0)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if s := l.StableOffset(); s < prev {
				t.Errorf("稳定位点回退: %d -> %d", prev, s)
				return
			} else {
				prev = s
			}
		}
	}()

	// 读者：结果只含已提交数据，且同一读取内位点递增。
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 500; i++ {
				recs, err := l.ReadCommitted(0, 1<<30)
				if err != nil {
					t.Errorf("read: %v", err)
					return
				}
				prev := int64(-1)
				for _, rec := range recs {
					if rec.Kind != KindData {
						t.Errorf("读到控制标记: %+v", rec)
						return
					}
					if strings.HasPrefix(rec.ProducerID, "aborter-") {
						t.Errorf("读到中止事务数据: %+v", rec)
						return
					}
					if rec.Offset <= prev {
						t.Errorf("读取结果位点非递增: %d -> %d", prev, rec.Offset)
						return
					}
					prev = rec.Offset
				}
			}
		}()
	}
	readers.Wait()
	close(stop)
	wg.Wait()

	// 全部写入结束后推进到末尾，校验最终读取恰好是全部提交型数据。
	total := int64((committers + aborters) * perProducer * 2)
	if err := l.AdvanceHighWatermark(total); err != nil {
		t.Fatalf("最终推进失败: %v", err)
	}
	if got := l.StableOffset(); got != total {
		t.Fatalf("最终稳定位点应为 %d, got %d", total, got)
	}
	recs, err := l.ReadCommitted(0, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != committers*perProducer {
		t.Fatalf("最终已提交数据应为 %d 条, got %d", committers*perProducer, len(recs))
	}
}
