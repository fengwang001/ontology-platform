package barrieralign

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func rec(ch int, key string) Event { return Event{Channel: ch, Key: key} }

func bar(ch int, n int64) Event { return Event{Channel: ch, Barrier: n} }

// logIO 打印输入、输出片段、缓冲与快照，作为每条判定的依据。
func logIO(t *testing.T, stage string, in []Event, a *Aligner) {
	t.Helper()
	parts := make([]string, len(in))
	for i, e := range in {
		if e.Barrier != 0 {
			parts[i] = fmt.Sprintf("c%d#b%d", e.Channel, e.Barrier)
		} else {
			parts[i] = fmt.Sprintf("c%d:%s", e.Channel, e.Key)
		}
	}
	out := a.Outputs()
	tail := out
	if len(tail) > 12 {
		tail = tail[len(tail)-12:]
	}
	outs := make([]string, len(tail))
	for i, o := range tail {
		if o.Kind == "barrier" {
			outs[i] = fmt.Sprintf("=>barrier%d(aligned=%v)", o.Barrier, o.Aligned)
		} else {
			outs[i] = fmt.Sprintf("=>c%d:%s", o.Channel, o.Key)
		}
	}
	p0, p1 := a.Pending()
	snaps := a.Snapshots()
	snapStr := make([]string, len(snaps))
	for i, s := range snaps {
		snapStr[i] = fmt.Sprintf("b%d=%v", s.Barrier, s.Counts)
	}
	t.Logf("[%s] 输入=[%s] 输出尾段=[%s] 待缓冲=(%d,%d) 快照=[%s]",
		stage, strings.Join(parts, " "), strings.Join(outs, " "), p0, p1, strings.Join(snapStr, " "))
}

func rejectKind(err error) RejectKind {
	if err == nil {
		return ""
	}
	if re, ok := err.(*RejectError); ok {
		return re.Kind
	}
	return "other"
}

// TestBlockAndBuffer 一侧先收到屏障后持续缓冲，未阻塞侧记录到达即处理。
func TestBlockAndBuffer(t *testing.T) {
	a := New(4)
	seq := []Event{
		bar(0, 1),                                // c0 立即阻塞
		rec(0, "a0"), rec(0, "a1"), rec(0, "a2"), // c0 记录全部缓冲
		rec(1, "b0"), rec(1, "b1"), // c1 未阻塞，到达即处理
	}
	if err := a.ProcessBatch(seq); err != nil {
		t.Fatalf("合法序列被拒绝: %v", err)
	}
	logIO(t, "阻塞等待对齐", seq, a)

	p0, p1 := a.Pending()
	if p0 != 3 || p1 != 0 {
		t.Fatalf("缓冲计数错误: got (%d,%d), want (3,0)", p0, p1)
	}
	t.Logf("判定: c0 屏障后 3 条记录被缓冲(pending=3)，c1 未阻塞 2 条立即输出(pending=0)")

	out := a.Outputs()
	if len(out) != 2 {
		t.Fatalf("对齐前输出条数错误: got %d, want 2", len(out))
	}
	if snaps := a.Snapshots(); len(snaps) != 0 {
		t.Fatalf("未对齐不应产生快照, got %d", len(snaps))
	}

	// c1 屏障到达：对齐拍快照，快照只含屏障之前已处理的 b0/b1。
	if err := a.Process(bar(1, 1)); err != nil {
		t.Fatalf("对齐屏障被拒绝: %v", err)
	}
	logIO(t, "对齐完成", []Event{bar(1, 1)}, a)
	snaps := a.Snapshots()
	if len(snaps) != 1 || snaps[0].Barrier != 1 {
		t.Fatalf("对齐后应有 1 张编号 1 的快照, got %v", snaps)
	}
	if snaps[0].Counts["b0"] != 1 || snaps[0].Counts["b1"] != 1 {
		t.Fatalf("快照应包含未阻塞侧两条记录, got %v", snaps[0].Counts)
	}
	if _, ok := snaps[0].Counts["a0"]; ok {
		t.Fatalf("缓冲记录 a0 不应出现在屏障 1 的快照中, got %v", snaps[0].Counts)
	}
	out = a.Outputs()
	if out[2].Kind != "barrier" || out[2].Barrier != 1 || !out[2].Aligned {
		t.Fatalf("快照之后应先转发对齐屏障, got %+v", out[2])
	}
	keys := []string{out[3].Key, out[4].Key, out[5].Key}
	if keys[0] != "a0" || keys[1] != "a1" || keys[2] != "a2" {
		t.Fatalf("缓冲应按到达顺序重放, got %v", keys)
	}
	p0, p1 = a.Pending()
	if p0 != 0 || p1 != 0 {
		t.Fatalf("重放后缓冲应清空, got (%d,%d)", p0, p1)
	}
	t.Log("判定: 快照={b0:1,b1:1}，先转发 barrier1，再按 a0,a1,a2 顺序重放，缓冲清空")
}

// TestMultiEpochAlignment 连续两个对齐周期，并验证重放中遇到下一号屏障重新阻塞。
func TestMultiEpochAlignment(t *testing.T) {
	a := New(8)
	// c0 先到 barrier1，缓冲 a 与 barrier2；c1 记录 x 立即处理。
	// c1 barrier1 触发对齐：重放 a 后在队列中遇到 c0 的 barrier2，重新阻塞。
	seq := []Event{
		bar(0, 1), rec(0, "a"), bar(0, 2),
		rec(1, "x"), bar(1, 1),
	}
	if err := a.ProcessBatch(seq); err != nil {
		t.Fatalf("合法序列被拒绝: %v", err)
	}
	logIO(t, "第一轮对齐+重放遇下一号屏障", seq, a)

	snaps := a.Snapshots()
	if len(snaps) != 1 || snaps[0].Barrier != 1 {
		t.Fatalf("应只有快照 1, got %v", snaps)
	}
	if snaps[0].Counts["x"] != 1 {
		t.Fatalf("快照1应含 x:1, got %v", snaps[0].Counts)
	}
	if _, ok := snaps[0].Counts["a"]; ok {
		t.Fatalf("缓冲记录 a 不属于快照1, got %v", snaps[0].Counts)
	}

	// c1 barrier2 与重放时已阻塞的 c0 barrier2 对齐。
	if err := a.Process(bar(1, 2)); err != nil {
		t.Fatalf("第二轮对齐屏障被拒绝: %v", err)
	}
	logIO(t, "第二轮对齐", []Event{bar(1, 2)}, a)
	snaps = a.Snapshots()
	if len(snaps) != 2 || snaps[1].Barrier != 2 {
		t.Fatalf("应有快照 1、2, got %v", snaps)
	}
	if snaps[1].Counts["a"] != 1 || snaps[1].Counts["x"] != 1 {
		t.Fatalf("快照2应含重放后的 a 与先到的 x, got %v", snaps[1].Counts)
	}

	out := a.Outputs()
	var barriers []int64
	for _, o := range out {
		if o.Kind == "barrier" {
			barriers = append(barriers, o.Barrier)
		}
	}
	if len(barriers) != 2 || barriers[0] != 1 || barriers[1] != 2 {
		t.Fatalf("下游应依次收到 barrier1、barrier2, got %v", barriers)
	}
	t.Log("判定: barrier1/barrier2 均在对齐后转发，重放遇 barrier2 时 c0 重新阻塞直至第二轮对齐")
}

// TestInvalidInputs 四类非法输入必须给出可区分原因。
func TestInvalidInputs(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
		want RejectKind
	}{
		{"非法通道号 2", Event{Channel: 2, Key: "k"}, RejectInvalidChannel},
		{"非法通道号 -1", Event{Channel: -1, Key: "k"}, RejectInvalidChannel},
		{"空键", Event{Channel: 0, Key: ""}, RejectEmptyKey},
		{"首道屏障编号错误", Event{Channel: 0, Barrier: 2}, RejectUnexpectedBarrier},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := New(4)
			err := a.Process(tc.ev)
			logIO(t, tc.name, []Event{tc.ev}, a)
			if rejectKind(err) != tc.want {
				t.Fatalf("%s: got %v, want %s", tc.name, err, tc.want)
			}
			t.Logf("判定: 错误原因=%s（%v），且无任何输出/快照", tc.want, err)
			if len(a.Outputs()) != 0 || len(a.Snapshots()) != 0 {
				t.Fatalf("被拒绝事件不得产生输出或快照")
			}
		})
	}

	// 已收到 barrier1 后再收到非下一号屏障（跳号 barrier3）。
	a := New(4)
	if err := a.Process(bar(0, 1)); err != nil {
		t.Fatal(err)
	}
	err := a.Process(bar(0, 3))
	logIO(t, "跳号屏障 barrier3", []Event{bar(0, 3)}, a)
	if rejectKind(err) != RejectUnexpectedBarrier {
		t.Fatalf("跳号屏障应拒绝, got %v", err)
	}
	t.Log("判定: c0 下一编号应为 2，barrier3 以 unexpected_barrier 拒绝")
}

// TestBufferOverflow 缓冲超限拒绝且状态不变。
func TestBufferOverflow(t *testing.T) {
	a := New(2)
	ok := []Event{bar(0, 1), rec(0, "a"), rec(0, "b")}
	if err := a.ProcessBatch(ok); err != nil {
		t.Fatal(err)
	}
	logIO(t, "缓冲达到上限 2", ok, a)

	before := len(a.Outputs())
	err := a.Process(rec(0, "c"))
	logIO(t, "第 3 条缓冲记录", []Event{rec(0, "c")}, a)
	if rejectKind(err) != RejectBufferOverflow {
		t.Fatalf("超限应报 buffer_overflow, got %v", err)
	}
	p0, _ := a.Pending()
	if p0 != 2 {
		t.Fatalf("拒绝后缓冲仍应为 2, got %d", p0)
	}
	if len(a.Outputs()) != before {
		t.Fatalf("拒绝后输出流不得变化")
	}
	t.Log("判定: 第 3 条以 buffer_overflow 拒绝，缓冲仍为 2，输出流不变")
}

// TestBatchAtomicity 批次中任意一条非法，整批不得改变任何状态。
func TestBatchAtomicity(t *testing.T) {
	good := []Event{rec(0, "x"), rec(1, "y")}
	a := New(2)
	if err := a.ProcessBatch(good); err != nil {
		t.Fatal(err)
	}
	baseOut := len(a.Outputs())
	baseSnaps := len(a.Snapshots())

	bad := []Event{bar(0, 1), rec(0, "buffered"), rec(2, "bad"), rec(0, "more")}
	err := a.ProcessBatch(bad)
	logIO(t, "含非法通道的混合批次", bad, a)
	if rejectKind(err) != RejectInvalidChannel {
		t.Fatalf("应整批拒绝为 invalid_channel, got %v", err)
	}
	if len(a.Outputs()) != baseOut || len(a.Snapshots()) != baseSnaps {
		t.Fatalf("被拒绝批次不得改变输出流或快照")
	}
	p0, p1 := a.Pending()
	if p0 != 0 || p1 != 0 {
		t.Fatalf("被拒绝批次不得留下缓冲, got (%d,%d)", p0, p1)
	}
	t.Log("判定: 批次在深拷贝副本上推演，提交前失败，累加状态/缓冲/快照/输出均不变")

	bad2 := []Event{bar(1, 1), rec(1, "q"), rec(1, "r"), rec(1, "s")}
	err = a.ProcessBatch(bad2)
	logIO(t, "同批超限", bad2, a)
	if rejectKind(err) != RejectBufferOverflow {
		t.Fatalf("应整批拒绝为 buffer_overflow, got %v", err)
	}
	if len(a.Outputs()) != baseOut {
		t.Fatalf("超限批次回滚后输出流应不变")
	}
}

// TestDeterminism 同一输入序列反复计算必须得到完全相同的输出与快照。
func TestDeterminism(t *testing.T) {
	seq := []Event{
		rec(0, "k1"), rec(1, "k1"),
		bar(0, 1), rec(0, "k2"), rec(0, "k1"),
		rec(1, "k2"), bar(1, 1),
		bar(0, 2), rec(0, "k1"),
		rec(1, "k1"), bar(1, 2),
	}
	run := func() ([]OutputEvent, []Snapshot) {
		a := New(8)
		if err := a.ProcessBatch(seq); err != nil {
			t.Fatal(err)
		}
		return a.Outputs(), a.Snapshots()
	}
	out1, snap1 := run()
	out2, snap2 := run()
	if fmt.Sprint(out1) != fmt.Sprint(out2) || fmt.Sprint(snap1) != fmt.Sprint(snap2) {
		t.Fatalf("两次计算结果不一致:\nout=%v vs %v\nsnaps=%v vs %v", out1, out2, snap1, snap2)
	}
	a := New(8)
	_ = a.ProcessBatch(seq)
	logIO(t, "确定性序列", seq, a)
	t.Logf("判定: 同一序列两次计算输出(%d 条)与快照(%d 张)完全一致", len(out1), len(snap1))
}

// TestConcurrentReads 写入过程中并发读取，race 检测器下验证逐键一致的只读视图。
func TestConcurrentReads(t *testing.T) {
	a := New(64)
	var wg sync.WaitGroup
	wg.Add(3)

	go func() { // 双通道交替写入，反复触发对齐
		defer wg.Done()
		for n := int64(1); n <= 20; n++ {
			_ = a.Process(bar(0, n))
			_ = a.ProcessBatch([]Event{rec(0, "k0"), rec(0, "k1")})
			_ = a.Process(rec(1, "k0"))
			_ = a.Process(bar(1, n))
		}
	}()

	reader := func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			snaps := a.Snapshots()
			for _, s := range snaps {
				if s.Barrier <= 0 || s.Counts == nil {
					t.Errorf("读到不完整快照: %+v", s)
					return
				}
				// 逐键一致：每个返回快照的 map 可独立、完整遍历。
				total := 0
				for _, v := range s.Counts {
					total += v
				}
				if total < 0 {
					t.Errorf("快照计数异常: %v", s.Counts)
					return
				}
			}
			_ = a.Outputs()
			_, _ = a.Pending()
		}
	}
	go reader()
	go reader()

	wg.Wait()
	snaps := a.Snapshots()
	if len(snaps) != 20 {
		t.Fatalf("应完成 20 次对齐, got %d", len(snaps))
	}
	logIO(t, "并发读写结束", nil, a)
	t.Logf("判定: -race 下 2 个读取协程并发遍历 20 张快照与输出流，无数据竞争且快照自洽")
}
