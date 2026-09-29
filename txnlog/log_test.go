package txnlog

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

// mustData 追加数据记录并校验返回位点。
func mustData(t *testing.T, l *Log, producer, payload string, wantOffset int64) {
	t.Helper()
	off, err := l.AppendData(producer, []byte(payload))
	if err != nil {
		t.Fatalf("AppendData(%q, %q) 出错: %v", producer, payload, err)
	}
	if off != wantOffset {
		t.Fatalf("AppendData(%q, %q) 位点 = %d, 期望 %d", producer, payload, off, wantOffset)
	}
	t.Logf("step: AppendData producer=%q payload=%q -> offset=%d stable=%d hw=%d",
		producer, payload, off, l.StableOffset(), l.HighWatermark())
}

// mustMarker 追加提交/中止标记并校验返回位点。
func mustMarker(t *testing.T, l *Log, producer string, commit bool, wantOffset int64) {
	t.Helper()
	off, err := l.AppendMarker(producer, commit)
	if err != nil {
		t.Fatalf("AppendMarker(%q, commit=%v) 出错: %v", producer, commit, err)
	}
	if off != wantOffset {
		t.Fatalf("AppendMarker(%q, commit=%v) 位点 = %d, 期望 %d", producer, commit, off, wantOffset)
	}
	t.Logf("step: AppendMarker producer=%q commit=%v -> offset=%d stable=%d hw=%d",
		producer, commit, off, l.StableOffset(), l.HighWatermark())
}

// mustHW 推进高水位。
func mustHW(t *testing.T, l *Log, hw int64) {
	t.Helper()
	if err := l.AdvanceHighWatermark(hw); err != nil {
		t.Fatalf("AdvanceHighWatermark(%d) 出错: %v", hw, err)
	}
	t.Logf("step: AdvanceHighWatermark -> hw=%d stable=%d", hw, l.StableOffset())
}

// mustRead 已提交读并校验返回的 payload 序列。
func mustRead(t *testing.T, l *Log, from int64, limit int, want ...string) {
	t.Helper()
	recs, err := l.ReadCommitted(from, limit)
	if err != nil {
		t.Fatalf("ReadCommitted(%d, %d) 出错: %v", from, limit, err)
	}
	got := make([]string, 0, len(recs))
	for _, r := range recs {
		if r.Kind != KindData {
			t.Fatalf("已提交读返回了控制标记: %+v", r)
		}
		got = append(got, string(r.Payload))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ReadCommitted(%d, %d) = %v, 期望 %v", from, limit, got, want)
	}
	t.Logf("step: ReadCommitted from=%d limit=%d -> %v (stable=%d)", from, limit, got, l.StableOffset())
}

// mustStable 校验稳定位点。
func mustStable(t *testing.T, l *Log, want int64, why string) {
	t.Helper()
	if got := l.StableOffset(); got != want {
		t.Fatalf("StableOffset = %d, 期望 %d（判定依据: %s）", got, want, why)
	}
	t.Logf("check: stable=%d 判定依据: %s", want, why)
}

// TestCommitThenAbortSameProducer 同一生产者先提交后中止的两个事务：
// 已提交读只返回第一个事务的数据。
func TestCommitThenAbortSameProducer(t *testing.T) {
	l := New()

	// 事务 1：d0 d1，提交。
	mustData(t, l, "p1", "d0", 0)
	mustData(t, l, "p1", "d1", 1)
	mustMarker(t, l, "p1", true, 2)
	// 事务 2：d2 d3，中止。
	mustData(t, l, "p1", "d2", 3)
	mustData(t, l, "p1", "d3", 4)
	mustMarker(t, l, "p1", false, 5)

	mustStable(t, l, 0, "hw=0，一切不可见")
	mustHW(t, l, 6)
	mustStable(t, l, 6, "hw=6 且两个事务的结论（标记 2、5）均低于 hw，无未决事务")

	mustRead(t, l, 0, 100, "d0", "d1")
	// 可复现：同样的读取再次执行结果一致。
	mustRead(t, l, 0, 100, "d0", "d1")
	// 从中途读取。
	mustRead(t, l, 1, 100, "d1")
	// limit 截断。
	mustRead(t, l, 0, 1, "d0")
}

// TestStableOffsetAdvance 交错写入下稳定位点的推进规则。
func TestStableOffsetAdvance(t *testing.T) {
	l := New()

	// p1 事务首位点 0；p2 事务首位点 2。
	mustData(t, l, "p1", "a0", 0)
	mustData(t, l, "p1", "a1", 1)
	mustData(t, l, "p2", "b0", 2)
	mustMarker(t, l, "p2", true, 3) // p2 提交，标记位点 3
	mustData(t, l, "p1", "a2", 4)
	mustMarker(t, l, "p1", true, 5) // p1 提交，标记位点 5

	// hw=2：p1 事务首位点 0 < hw 且未决（标记 5 未写入前不可见）——
	// 但标记已写入位点 5，只是 5 >= hw，结论不可见，稳定位点被压到 0。
	mustHW(t, l, 2)
	mustStable(t, l, 0, "p1 事务首位点 0 < hw=2，其标记 5 >= hw 结论不可见")
	mustRead(t, l, 0, 100) // 空

	// hw=4：p1 标记 5 仍不可见，稳定位点仍为 0。
	mustHW(t, l, 4)
	mustStable(t, l, 0, "p1 标记 5 >= hw=4，结论仍不可见")

	// hw=6：两个事务结论均可见，稳定位点推进到 6。
	mustHW(t, l, 6)
	mustStable(t, l, 6, "p1 标记 5 < hw=6，p2 标记 3 < hw=6，无未决事务")
	mustRead(t, l, 0, 100, "a0", "a1", "b0", "a2")

	// 新事务未决会再次压住稳定位点。
	mustData(t, l, "p3", "c0", 6)
	mustStable(t, l, 6, "p3 事务首位点 6 = 稳定位点，未决")
	mustMarker(t, l, "p3", false, 7) // 中止
	mustStable(t, l, 6, "p3 标记 7 >= hw=6，结论不可见")
	mustHW(t, l, 8)
	mustStable(t, l, 8, "p3 中止标记 7 < hw=8，无未决事务")
	mustRead(t, l, 0, 100, "a0", "a1", "b0", "a2") // 中止事务数据不返回
}

// snapshot 捕获日志的可观察状态，用于验证拒绝后状态不变。
type snapshot struct {
	leo, hw, stable int64
	read            []string
}

func takeSnapshot(t *testing.T, l *Log) snapshot {
	t.Helper()
	recs, err := l.ReadCommitted(0, 1<<30)
	if err != nil {
		t.Fatalf("快照读取失败: %v", err)
	}
	s := snapshot{leo: l.LogEndOffset(), hw: l.HighWatermark(), stable: l.StableOffset()}
	for _, r := range recs {
		s.read = append(s.read, string(r.Payload))
	}
	return s
}

func (s snapshot) assertUnchanged(t *testing.T, l *Log, op string) {
	t.Helper()
	now := takeSnapshot(t, l)
	if now.leo != s.leo || now.hw != s.hw || now.stable != s.stable || !reflect.DeepEqual(now.read, s.read) {
		t.Fatalf("拒绝 %s 后状态发生变化: 之前 %+v, 之后 %+v", op, s, now)
	}
	t.Logf("check: 拒绝 %s 后状态不变 leo=%d hw=%d stable=%d read=%v", op, now.leo, now.hw, now.stable, now.read)
}

// TestInvalidInputsRejected 各类非法输入整体拒绝、原因可区分、失败不留痕。
func TestInvalidInputsRejected(t *testing.T) {
	l := New()
	mustData(t, l, "p1", "d0", 0)
	mustMarker(t, l, "p1", true, 1)
	mustHW(t, l, 2)
	before := takeSnapshot(t, l)

	// 1. 非法生产者：空标识。
	if _, err := l.AppendData("", []byte("x")); !errors.Is(err, ErrInvalidProducer) {
		t.Fatalf("空生产者 AppendData 错误 = %v, 期望 ErrInvalidProducer", err)
	}
	before.assertUnchanged(t, l, "AppendData(空生产者)")
	if _, err := l.AppendMarker("", true); !errors.Is(err, ErrInvalidProducer) {
		t.Fatalf("空生产者 AppendMarker 错误 = %v, 期望 ErrInvalidProducer", err)
	}
	before.assertUnchanged(t, l, "AppendMarker(空生产者)")

	// 2. 无进行中事务却写标记：p1 事务已提交结束；p2 从未写数据。
	if _, err := l.AppendMarker("p1", true); !errors.Is(err, ErrNoOngoingTransaction) {
		t.Fatalf("重复标记错误 = %v, 期望 ErrNoOngoingTransaction", err)
	}
	before.assertUnchanged(t, l, "AppendMarker(无进行中事务)")
	if _, err := l.AppendMarker("p2", false); !errors.Is(err, ErrNoOngoingTransaction) {
		t.Fatalf("未知生产者标记错误 = %v, 期望 ErrNoOngoingTransaction", err)
	}
	before.assertUnchanged(t, l, "AppendMarker(未知生产者)")

	// 3. 非法高水位：回退、越过日志末端。
	if err := l.AdvanceHighWatermark(1); !errors.Is(err, ErrInvalidHighWatermark) {
		t.Fatalf("高水位回退错误 = %v, 期望 ErrInvalidHighWatermark", err)
	}
	before.assertUnchanged(t, l, "AdvanceHighWatermark(回退)")
	if err := l.AdvanceHighWatermark(3); !errors.Is(err, ErrInvalidHighWatermark) {
		t.Fatalf("高水位越界错误 = %v, 期望 ErrInvalidHighWatermark", err)
	}
	before.assertUnchanged(t, l, "AdvanceHighWatermark(越过日志末端)")

	// 4. 非法读取起点：负值、超过稳定位点。
	if _, err := l.ReadCommitted(-1, 10); !errors.Is(err, ErrInvalidReadStart) {
		t.Fatalf("负起点错误 = %v, 期望 ErrInvalidReadStart", err)
	}
	before.assertUnchanged(t, l, "ReadCommitted(负起点)")
	if _, err := l.ReadCommitted(l.StableOffset()+1, 10); !errors.Is(err, ErrInvalidReadStart) {
		t.Fatalf("超过稳定位点错误 = %v, 期望 ErrInvalidReadStart", err)
	}
	before.assertUnchanged(t, l, "ReadCommitted(超过稳定位点)")

	// 5. 非法读取条数。
	if _, err := l.ReadCommitted(0, 0); !errors.Is(err, ErrInvalidReadLimit) {
		t.Fatalf("limit=0 错误 = %v, 期望 ErrInvalidReadLimit", err)
	}
	before.assertUnchanged(t, l, "ReadCommitted(limit=0)")

	// 错误类别互不相同、可区分。
	all := []error{ErrInvalidProducer, ErrNoOngoingTransaction, ErrInvalidHighWatermark, ErrInvalidReadStart, ErrInvalidReadLimit}
	for i := range all {
		for j := range all {
			if i != j && errors.Is(all[i], all[j]) {
				t.Fatalf("错误类别不可区分: %v 与 %v", all[i], all[j])
			}
		}
	}
}

// TestConcurrentAccess 多执行体并发追加、推进高水位与读取：
// 位点连续、稳定位点只进不退、读取绝不暴露未决或中止数据。
func TestConcurrentAccess(t *testing.T) {
	l := New()
	const producers = 4
	const txnsPerProducer = 25

	var wwg sync.WaitGroup // 写入者
	var owg sync.WaitGroup // 观察者（高水位推进、稳定位点观察、读取）
	stop := make(chan struct{})
	errCh := make(chan string, 64)

	// 写入者：每个生产者交错提交与中止事务。
	for p := 0; p < producers; p++ {
		wwg.Add(1)
		go func(p int) {
			defer wwg.Done()
			producer := fmt.Sprintf("p%d", p)
			for j := 0; j < txnsPerProducer; j++ {
				payload := fmt.Sprintf("%s-txn%d", producer, j)
				if _, err := l.AppendData(producer, []byte(payload)); err != nil {
					errCh <- fmt.Sprintf("AppendData: %v", err)
					return
				}
				commit := j%3 != 2 // 每三个事务中止一个
				if _, err := l.AppendMarker(producer, commit); err != nil {
					errCh <- fmt.Sprintf("AppendMarker: %v", err)
					return
				}
			}
		}(p)
	}

	// 高水位推进者：只进不退，直到日志末端。
	owg.Add(1)
	go func() {
		defer owg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			hw, leo := l.HighWatermark(), l.LogEndOffset()
			if hw < leo {
				if err := l.AdvanceHighWatermark(hw + 1); err != nil {
					errCh <- fmt.Sprintf("AdvanceHighWatermark(%d): %v", hw+1, err)
					return
				}
			}
		}
	}()

	// 稳定位点观察者：验证只进不退。
	owg.Add(1)
	go func() {
		defer owg.Done()
		prev := int64(0)
		for {
			select {
			case <-stop:
				return
			default:
			}
			s := l.StableOffset()
			if s < prev {
				errCh <- fmt.Sprintf("稳定位点回退: %d -> %d", prev, s)
				return
			}
			prev = s
		}
	}()

	// 读者：验证返回内容只含已提交事务的数据，且位点严格递增、无标记。
	owg.Add(1)
	go func() {
		defer owg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			recs, err := l.ReadCommitted(0, 1<<30)
			if err != nil {
				errCh <- fmt.Sprintf("ReadCommitted: %v", err)
				return
			}
			prev := int64(-1)
			for _, r := range recs {
				if r.Kind != KindData {
					errCh <- fmt.Sprintf("读取返回控制标记: %+v", r)
					return
				}
				if r.Offset <= prev {
					errCh <- fmt.Sprintf("读取位点乱序: %d -> %d", prev, r.Offset)
					return
				}
				prev = r.Offset
				var p, j int
				if n, _ := fmt.Sscanf(string(r.Payload), "p%d-txn%d", &p, &j); n != 2 {
					errCh <- fmt.Sprintf("未知 payload: %q", r.Payload)
					return
				}
				if j%3 == 2 {
					errCh <- fmt.Sprintf("读取暴露了中止事务数据: %q", r.Payload)
					return
				}
			}
		}
	}()

	// 先等写入者全部完成并停止观察者，再把高水位推进到末端做终态校验。
	wwg.Wait()
	close(stop)
	owg.Wait()
	if err := l.AdvanceHighWatermark(l.LogEndOffset()); err != nil {
		t.Fatalf("最终推进高水位失败: %v", err)
	}

	select {
	case msg := <-errCh:
		t.Fatal(msg)
	default:
	}

	// 终态校验：稳定位点 == 高水位 == 日志末端，读取可复现。
	if s, hw := l.StableOffset(), l.HighWatermark(); s != hw {
		t.Fatalf("终态稳定位点 %d != 高水位 %d", s, hw)
	}
	first, err := l.ReadCommitted(0, 1<<30)
	if err != nil {
		t.Fatalf("终态读取失败: %v", err)
	}
	second, err := l.ReadCommitted(0, 1<<30)
	if err != nil {
		t.Fatalf("终态复读失败: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("同一稳定位点下两次读取结果不一致，不可复现")
	}
	t.Logf("并发终态: leo=%d hw=%d stable=%d committed=%d 条", l.LogEndOffset(), l.HighWatermark(), l.StableOffset(), len(first))
}

// ---------- 批量参照模型：独立重放全部输入，逐步比对 ----------

// refRec 参照模型中的一条记录。
type refRec struct {
	kind     RecordKind
	producer string
	payload  string
}

// refModel 参照实现：只用最朴素的扫描重放，与被测实现相互独立。
type refModel struct {
	recs []refRec
	hw   int64
}

// stable 重放计算稳定位点，并给出判定依据。
func (m *refModel) stable() (int64, string) {
	type doneTxn struct{ first, marker int64 }
	ongoing := map[string]int64{}
	var dones []doneTxn
	for i, r := range m.recs {
		off := int64(i)
		if r.kind == KindData {
			if _, ok := ongoing[r.producer]; !ok {
				ongoing[r.producer] = off
			}
		} else {
			first := ongoing[r.producer]
			delete(ongoing, r.producer)
			dones = append(dones, doneTxn{first, off})
		}
	}
	stable := m.hw
	why := fmt.Sprintf("hw=%d 且无阻碍事务", m.hw)
	for p, first := range ongoing {
		if first < stable {
			stable = first
			why = fmt.Sprintf("生产者 %s 事务未决，首位点 %d", p, first)
		}
	}
	for _, d := range dones {
		if d.marker >= m.hw && d.first < stable {
			stable = d.first
			why = fmt.Sprintf("事务标记 %d 不低于 hw=%d 结论不可见，首位点 %d", d.marker, m.hw, d.first)
		}
	}
	return stable, why
}

// committedPayloads 重放计算稳定位点之前已提交事务的数据 payload。
func (m *refModel) committedPayloads() []string {
	stable, _ := m.stable()
	committed := make([]bool, len(m.recs))
	ongoing := map[string][]int64{}
	for i, r := range m.recs {
		if r.kind == KindData {
			ongoing[r.producer] = append(ongoing[r.producer], int64(i))
		} else {
			for _, o := range ongoing[r.producer] {
				committed[o] = r.kind == KindCommit
			}
			delete(ongoing, r.producer)
		}
	}
	out := []string{}
	for off := int64(0); off < stable; off++ {
		if m.recs[off].kind == KindData && committed[off] {
			out = append(out, m.recs[off].payload)
		}
	}
	return out
}

// TestBatchReferenceConsistency 批量脚本逐步执行，每步打印输入、稳定位点与判定依据，
// 并与独立参照模型比对稳定位点和已提交读结果。
func TestBatchReferenceConsistency(t *testing.T) {
	type op struct {
		desc string
		run  func(l *Log, m *refModel) error
	}
	data := func(p, payload string) op {
		return op{"AppendData " + p + " " + payload, func(l *Log, m *refModel) error {
			_, err := l.AppendData(p, []byte(payload))
			if err == nil {
				m.recs = append(m.recs, refRec{KindData, p, payload})
			}
			return err
		}}
	}
	marker := func(p string, commit bool) op {
		word := "abort"
		if commit {
			word = "commit"
		}
		return op{"AppendMarker " + p + " " + word, func(l *Log, m *refModel) error {
			_, err := l.AppendMarker(p, commit)
			if err == nil {
				kind := KindAbort
				if commit {
					kind = KindCommit
				}
				m.recs = append(m.recs, refRec{kind, p, ""})
			}
			return err
		}}
	}
	hw := func(v int64) op {
		return op{fmt.Sprintf("AdvanceHW %d", v), func(l *Log, m *refModel) error {
			err := l.AdvanceHighWatermark(v)
			if err == nil {
				m.hw = v
			}
			return err
		}}
	}

	ops := []op{
		data("p1", "a0"), data("p1", "a1"), // p1 事务 1
		data("p2", "b0"),                   // p2 事务 1 开始，交错
		marker("p1", true),                 // p1 提交
		data("p1", "a2"), data("p1", "a3"), // p1 事务 2
		marker("p1", false), // p1 中止（同一生产者先提交后中止）
		marker("p2", true),  // p2 提交
		hw(3), hw(5), hw(8),
		data("p3", "c0"), // 未决事务压住稳定位点
		hw(9),
		marker("p3", true),
		hw(10),
		data("p2", "b1"), marker("p2", false), // p2 中止事务
		hw(12),
		// 非法输入：整体拒绝且参照模型不变。
		data("", "bad"),
		marker("p9", true),
		hw(1),
		hw(99),
	}

	l := New()
	m := &refModel{}
	for i, o := range ops {
		err := o.run(l, m)
		wantStable, why := m.stable()
		gotStable := l.StableOffset()
		t.Logf("step %02d: 输入=%s 拒绝=%v 稳定位点=%d 判定依据: %s", i, o.desc, err != nil, gotStable, why)
		if gotStable != wantStable {
			t.Fatalf("step %02d (%s): 稳定位点 = %d, 参照 = %d（%s）", i, o.desc, gotStable, wantStable, why)
		}
		// 已提交读与参照一致。
		recs, rerr := l.ReadCommitted(0, 1<<30)
		if rerr != nil {
			t.Fatalf("step %02d: ReadCommitted 出错: %v", i, rerr)
		}
		got := make([]string, 0, len(recs))
		for _, r := range recs {
			got = append(got, string(r.Payload))
		}
		want := m.committedPayloads()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %02d (%s): 已提交读 = %v, 参照 = %v", i, o.desc, got, want)
		}
		t.Logf("step %02d: 已提交读=%v", i, got)
	}

	// 非法读取起点同样整体拒绝。
	if _, err := l.ReadCommitted(l.StableOffset()+1, 5); !errors.Is(err, ErrInvalidReadStart) {
		t.Fatalf("非法读取起点错误 = %v, 期望 ErrInvalidReadStart", err)
	}
	t.Logf("终态: leo=%d hw=%d stable=%d", l.LogEndOffset(), l.HighWatermark(), l.StableOffset())
}

// TestReadCommittedReproducible 同一稳定位点下重复读取结果完全一致。
func TestReadCommittedReproducible(t *testing.T) {
	l := New()
	mustData(t, l, "p1", "x0", 0)
	mustMarker(t, l, "p1", true, 1)
	mustData(t, l, "p1", "x1", 2)
	mustMarker(t, l, "p1", false, 3)
	mustHW(t, l, 4)

	var prev []Record
	for i := 0; i < 5; i++ {
		recs, err := l.ReadCommitted(0, 100)
		if err != nil {
			t.Fatalf("第 %d 次读取失败: %v", i, err)
		}
		if prev != nil && !reflect.DeepEqual(prev, recs) {
			t.Fatalf("第 %d 次读取与前次不一致", i)
		}
		prev = recs
	}
	if len(prev) != 1 || string(prev[0].Payload) != "x0" {
		t.Fatalf("已提交读 = %v, 期望仅 [x0]", prev)
	}
	// 从稳定位点本身读取为空（合法边界）。
	recs, err := l.ReadCommitted(l.StableOffset(), 10)
	if err != nil || len(recs) != 0 {
		t.Fatalf("从稳定位点读取 = %v, %v, 期望空且无错误", recs, err)
	}
	t.Logf("复现性校验通过: 5 次读取均为 [x0]，stable=%d", l.StableOffset())
}

// TestNoControlMarkersExposed 控制标记在任何读取中都不出现。
func TestNoControlMarkersExposed(t *testing.T) {
	l := New()
	mustData(t, l, "p1", "m0", 0)
	mustMarker(t, l, "p1", true, 1)
	mustData(t, l, "p2", "m1", 2)
	mustMarker(t, l, "p2", true, 3)
	mustHW(t, l, 4)
	recs, err := l.ReadCommitted(0, 100)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	for _, r := range recs {
		if r.Kind != KindData {
			t.Fatalf("读取暴露了控制标记: %+v", r)
		}
		if strings.HasPrefix(string(r.Payload), "marker") {
			t.Fatalf("读取暴露了标记内容: %q", r.Payload)
		}
	}
	t.Logf("控制标记校验通过: 读取 %d 条均为数据记录", len(recs))
}
