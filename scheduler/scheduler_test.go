package scheduler

import (
	"fmt"
	"reflect"
	"testing"
)

func newSched(t *testing.T, m int64) *Scheduler {
	t.Helper()
	s, err := New(m)
	if err != nil {
		t.Fatalf("New(%d): %v", m, err)
	}
	return s
}

func mustAdd(t *testing.T, s *Scheduler, id string, rtt, cwnd int64, role Role) {
	t.Helper()
	if _, err := s.AddSubflow(id, rtt, cwnd, role); err != nil {
		t.Fatalf("AddSubflow(%q): %v", id, err)
	}
}

func mustConnAck(t *testing.T, s *Scheduler, ack, win int64) *Decision {
	t.Helper()
	d, err := s.ConnAck(ack, win)
	if err != nil {
		t.Fatalf("ConnAck(%d,%d): %v", ack, win, err)
	}
	return d
}

func mustWrite(t *testing.T, s *Scheduler, n int64) *Decision {
	t.Helper()
	d, err := s.Write(n)
	if err != nil {
		t.Fatalf("Write(%d): %v", n, err)
	}
	return d
}

// sent 以 "seq:len@subflow(R)" 形式格式化本次发出的段，便于比对。
func sent(d *Decision) []string {
	var out []string
	for _, g := range d.Sent {
		mark := ""
		if g.Resend {
			mark = "(R)"
		}
		out = append(out, fmt.Sprintf("%d:%d@%s%s", g.Seq, g.Len, g.Subflow, mark))
	}
	return out
}

func wantSent(t *testing.T, d *Decision, want ...string) {
	t.Helper()
	got := sent(d)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("发出的段不符：got %v, want %v", got, want)
	}
}

func wantCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %s，实际无错误", code)
	}
	got, ok := CodeOf(err)
	if !ok || got != code {
		t.Fatalf("期望错误码 %s，实际 %v", code, err)
	}
}

func subflowOf(st State, id string) SubflowState {
	for _, sf := range st.Subflows {
		if sf.ID == id {
			return sf
		}
	}
	return SubflowState{ID: id}
}

// 普通子流优先于备用子流；只要存在活跃的普通子流（哪怕暂时已满），
// 备用子流就不参与选路；普通子流全部失效后备用子流才接管。
func TestBackupSwitchTiming(t *testing.T) {
	s := newSched(t, 100)
	mustAdd(t, s, "n1", 50, 200, Normal)
	mustAdd(t, s, "b1", 1, 10000, Backup)
	mustConnAck(t, s, 0, 100000)

	// 普通子流时延更高仍优先。
	d := mustWrite(t, s, 200)
	wantSent(t, d, "0:100@n1", "100:100@n1")

	// n1 拥塞窗口已满，但只要它活跃，b1 不得接管。
	d = mustWrite(t, s, 100)
	wantSent(t, d)
	if d.Blocked == "" {
		t.Fatalf("期望队首阻塞说明，实际为空")
	}

	// n1 失效：在途段重新注入到 b1，之后新数据也走 b1。
	d, err := s.SubflowFail("n1")
	if err != nil {
		t.Fatalf("SubflowFail: %v", err)
	}
	wantSent(t, d, "0:100@b1(R)", "100:100@b1(R)", "200:100@b1")
}

// 候选子流取往返时延最小者，并列取标识较小者。
func TestRTTTieBreak(t *testing.T) {
	s := newSched(t, 100)
	mustAdd(t, s, "c", 20, 1000, Normal)
	mustAdd(t, s, "a", 20, 1000, Normal)
	mustAdd(t, s, "b", 30, 1000, Normal)
	mustConnAck(t, s, 0, 100000)

	d := mustWrite(t, s, 100)
	wantSent(t, d, "0:100@a") // a 与 c 时延并列，取标识较小者

	if _, err := s.SubflowAck("a", 100); err != nil {
		t.Fatalf("SubflowAck: %v", err)
	}
	d = mustWrite(t, s, 100)
	wantSent(t, d, "100:100@a")

	if _, err := s.SetRTT("c", 5); err != nil {
		t.Fatalf("SetRTT: %v", err)
	}
	d = mustWrite(t, s, 100)
	wantSent(t, d, "200:100@c")

	if _, err := s.SetRTT("b", 1); err != nil {
		t.Fatalf("SetRTT: %v", err)
	}
	d = mustWrite(t, s, 100)
	wantSent(t, d, "300:100@b")
}

// 连接级窗口恰好够与差一字节的边界。
func TestWindowExactVsOneByteShort(t *testing.T) {
	s := newSched(t, 100)
	mustAdd(t, s, "a", 10, 100000, Normal)

	// 初始通告窗口为 0，新数据不可发。
	d := mustWrite(t, s, 100)
	wantSent(t, d)

	// 窗口 199：发一段后，[100,200) 差一字节不可发。
	d = mustConnAck(t, s, 0, 199)
	wantSent(t, d, "0:100@a")
	d = mustWrite(t, s, 100)
	wantSent(t, d)

	// 窗口恰好 200：[100,200) 恰好够。
	d = mustConnAck(t, s, 0, 200)
	wantSent(t, d, "100:100@a")

	// 确认推进到 100、窗口 199：上限 299，[200,300) 差一字节。
	d = mustWrite(t, s, 100)
	wantSent(t, d)
	d = mustConnAck(t, s, 100, 199)
	wantSent(t, d)
	// 窗口 200：上限 300，恰好够。
	d = mustConnAck(t, s, 100, 200)
	wantSent(t, d, "200:100@a")
}

// 失效时在途段按连接级序号升序重新注入，且优先于新数据。
func TestFailureReinjectionOrder(t *testing.T) {
	s := newSched(t, 10)
	mustAdd(t, s, "a", 5, 1000, Normal)
	mustAdd(t, s, "b", 10, 1000, Normal)
	mustConnAck(t, s, 0, 1000)

	d := mustWrite(t, s, 50)
	wantSent(t, d, "0:10@a", "10:10@a", "20:10@a", "30:10@a", "40:10@a")

	// 子流确认释放前两段，在途剩 [20,30) [30,40) [40,50)。
	if _, err := s.SubflowAck("a", 20); err != nil {
		t.Fatalf("SubflowAck: %v", err)
	}
	// 再写入两段（含尾段不足 M）。
	d = mustWrite(t, s, 15)
	wantSent(t, d, "50:10@a", "60:5@a")

	// 失效：五段在途按序号升序重新注入到 b。
	d, err := s.SubflowFail("a")
	if err != nil {
		t.Fatalf("SubflowFail: %v", err)
	}
	wantSent(t, d, "20:10@b(R)", "30:10@b(R)", "40:10@b(R)", "50:10@b(R)", "60:5@b(R)")
}

// 重新注入段排在待发队列最前，优先于尚未发过的新数据。
func TestReinjectPriorityOverFresh(t *testing.T) {
	s := newSched(t, 10)
	mustAdd(t, s, "a", 5, 100, Normal)
	mustConnAck(t, s, 0, 1000)
	d := mustWrite(t, s, 30)
	wantSent(t, d, "0:10@a", "10:10@a", "20:10@a")

	d, err := s.SubflowFail("a")
	if err != nil {
		t.Fatalf("SubflowFail: %v", err)
	}
	wantSent(t, d) // 无活跃子流，重新注入段等待

	d = mustWrite(t, s, 20) // 新数据排在重新注入段之后
	wantSent(t, d)

	d, err = s.AddSubflow("b", 5, 100, Normal)
	if err != nil {
		t.Fatalf("AddSubflow: %v", err)
	}
	// AddSubflow 本身也会触发发送决策：重新注入段先于新数据。
	wantSent(t, d, "0:10@b(R)", "10:10@b(R)", "20:10@b(R)", "30:10@b", "40:10@b")
	st := s.Snapshot()
	if len(st.PendingReinject) != 0 || len(st.PendingFresh) != 0 {
		t.Fatalf("队列应为空：%+v", st)
	}
	if got := subflowOf(st, "b").InflightBytes; got != 50 {
		t.Fatalf("b 的在途应为 50，实际 %d", got)
	}
	// 校验顺序：重新注入段先于新数据。
	ifst := subflowOf(st, "b").Inflight
	want := []Seg{{Seq: 0, Len: 10}, {Seq: 10, Len: 10}, {Seq: 20, Len: 10}, {Seq: 30, Len: 10}, {Seq: 40, Len: 10}}
	if !reflect.DeepEqual(ifst, want) {
		t.Fatalf("b 的在途顺序不符：got %v, want %v", ifst, want)
	}
}

// 失效子流的子流确认视为迟到：忽略、不报错、不改变任何状态。
func TestLateAckFromFailedSubflow(t *testing.T) {
	s := newSched(t, 10)
	mustAdd(t, s, "a", 5, 100, Normal)
	mustAdd(t, s, "b", 10, 100, Normal)
	mustConnAck(t, s, 0, 1000)
	mustWrite(t, s, 30)
	if _, err := s.SubflowFail("a"); err != nil {
		t.Fatalf("SubflowFail: %v", err)
	}
	before := s.Snapshot()

	// 迟到确认：不报错，状态不变。
	d, err := s.SubflowAck("a", 30)
	if err != nil {
		t.Fatalf("迟到确认不应报错：%v", err)
	}
	wantSent(t, d)
	if !reflect.DeepEqual(s.Snapshot(), before) {
		t.Fatalf("迟到确认改变了状态")
	}

	// 即使数值荒谬（远超曾在途），同样忽略。
	if _, err := s.SubflowAck("a", 1<<40); err != nil {
		t.Fatalf("迟到确认不应报错：%v", err)
	}
	if !reflect.DeepEqual(s.Snapshot(), before) {
		t.Fatalf("迟到确认改变了状态")
	}

	// 但参数非法仍按优先级最先报告。
	_, err = s.SubflowAck("a", -1)
	wantCode(t, err, ErrInvalidArgument)
}

// 窗口收缩不撤回已发送范围，只限制之后的新发送。
func TestWindowShrink(t *testing.T) {
	s := newSched(t, 100)
	mustAdd(t, s, "a", 10, 100000, Normal)
	d := mustConnAck(t, s, 0, 1000)
	wantSent(t, d)
	d = mustWrite(t, s, 1000)
	wantSent(t, d, "0:100@a", "100:100@a", "200:100@a", "300:100@a", "400:100@a",
		"500:100@a", "600:100@a", "700:100@a", "800:100@a", "900:100@a")

	// 窗口收缩到确认序号 500 + 窗口 100 = 600 < 已发送 1000：不撤回。
	d = mustConnAck(t, s, 500, 100)
	wantSent(t, d)
	if got := subflowOf(s.Snapshot(), "a").InflightBytes; got != 1000 {
		t.Fatalf("窗口收缩不得撤回在途，实际在途 %d", got)
	}
	// 新发送受收缩后的窗口限制。
	d = mustWrite(t, s, 100)
	wantSent(t, d)

	// 上限推进到 1000 仍不够 [1000,1100)。
	d = mustConnAck(t, s, 600, 400)
	wantSent(t, d)
	// 上限 1100：恰好够。
	d = mustConnAck(t, s, 600, 500)
	wantSent(t, d, "1000:100@a")

	// 过期确认：确认序号倒退，报错且不改变窗口。
	_, err := s.ConnAck(500, 99999)
	wantCode(t, err, ErrStaleAck)
	if got := s.Snapshot().Window; got != 500 {
		t.Fatalf("过期确认不得改变窗口，实际窗口 %d", got)
	}
}

// 错误分类与固定优先级：参数非法 > 子流不存在 > 过期确认 >
// 确认非整段 > 确认越界；被拒绝的事件不得改变任何状态。
func TestRejectionOrder(t *testing.T) {
	s := newSched(t, 100)
	mustAdd(t, s, "a", 10, 1000, Normal)
	mustConnAck(t, s, 0, 1000)
	mustWrite(t, s, 300) // 三段 [0,100) [100,200) [200,300) 全部在 a 上

	check := func(name string, f func() error, code ErrorCode) {
		t.Helper()
		before := s.Snapshot()
		err := f()
		wantCode(t, err, code)
		if after := s.Snapshot(); !reflect.DeepEqual(after, before) {
			t.Fatalf("%s：被拒绝的事件改变了状态\nbefore=%+v\nafter=%+v", name, before, after)
		}
	}

	// 参数非法优先于子流不存在。
	check("空标识+负确认", func() error {
		_, err := s.SubflowAck("", -5)
		return err
	}, ErrInvalidArgument)
	check("未知子流+负确认", func() error {
		_, err := s.SubflowAck("ghost", -5)
		return err
	}, ErrInvalidArgument)
	check("未知子流", func() error {
		_, err := s.SubflowAck("ghost", 5)
		return err
	}, ErrSubflowNotFound)
	check("负确认序号", func() error {
		_, err := s.ConnAck(-1, 100)
		return err
	}, ErrInvalidArgument)
	check("负窗口", func() error {
		_, err := s.ConnAck(0, -1)
		return err
	}, ErrInvalidArgument)

	// 确认越界：确认序号超出已发送范围（sentMax=300）。
	check("连接级确认越界", func() error {
		_, err := s.ConnAck(400, 100)
		return err
	}, ErrAckOutOfRange)

	// 子流确认：非整段与越界。
	check("子流确认非整段", func() error {
		_, err := s.SubflowAck("a", 150)
		return err
	}, ErrAckMisaligned)
	check("子流确认越界", func() error {
		_, err := s.SubflowAck("a", 400)
		return err
	}, ErrAckOutOfRange)

	// 先推进子流确认，再验证过期确认优先于非整段/越界。
	if _, err := s.SubflowAck("a", 100); err != nil {
		t.Fatalf("SubflowAck: %v", err)
	}
	check("子流过期确认", func() error {
		_, err := s.SubflowAck("a", 50)
		return err
	}, ErrStaleAck)

	// 连接级过期确认优先，且不得改变窗口。
	if _, err := s.ConnAck(100, 500); err != nil {
		t.Fatalf("ConnAck: %v", err)
	}
	check("连接级过期确认", func() error {
		_, err := s.ConnAck(50, 99999)
		return err
	}, ErrStaleAck)

	// 不存在的子流上的各类事件。
	check("失效未知子流", func() error {
		_, err := s.SubflowFail("ghost")
		return err
	}, ErrSubflowNotFound)
	check("恢复未知子流", func() error {
		_, err := s.SubflowRecover("ghost")
		return err
	}, ErrSubflowNotFound)
	check("修改未知子流时延", func() error {
		_, err := s.SetRTT("ghost", 5)
		return err
	}, ErrSubflowNotFound)
	check("非法时延优先于未知子流", func() error {
		_, err := s.SetRTT("ghost", 0)
		return err
	}, ErrInvalidArgument)
}

// 参数校验：配置与写入。
func TestValidation(t *testing.T) {
	if _, err := New(0); err == nil {
		t.Fatalf("New(0) 应报参数非法")
	} else if code, _ := CodeOf(err); code != ErrInvalidArgument {
		t.Fatalf("New(0) 错误码应为参数非法，实际 %v", err)
	}
	if _, err := New(-3); err == nil {
		t.Fatalf("New(-3) 应报参数非法")
	}

	s := newSched(t, 100)
	cases := []struct {
		name string
		f    func() error
	}{
		{"空标识", func() error { _, e := s.AddSubflow("", 10, 100, Normal); return e }},
		{"零时延", func() error { _, e := s.AddSubflow("x", 0, 100, Normal); return e }},
		{"负时延", func() error { _, e := s.AddSubflow("x", -1, 100, Normal); return e }},
		{"窗口小于段长", func() error { _, e := s.AddSubflow("x", 10, 99, Normal); return e }},
		{"非法角色", func() error { _, e := s.AddSubflow("x", 10, 100, Role(7)); return e }},
		{"零写入", func() error { _, e := s.Write(0); return e }},
		{"负写入", func() error { _, e := s.Write(-5); return e }},
	}
	for _, c := range cases {
		wantCode(t, c.f(), ErrInvalidArgument)
	}

	mustAdd(t, s, "x", 10, 100, Normal)
	if _, err := s.AddSubflow("x", 10, 100, Normal); true {
		wantCode(t, err, ErrInvalidArgument) // 重复标识
	}
}

// 重新注入段不得再选择失效来源子流，即使该子流已恢复。
func TestReinjectExcludesRecoveredSubflow(t *testing.T) {
	s := newSched(t, 100)
	mustAdd(t, s, "a", 10, 1000, Normal)
	mustConnAck(t, s, 0, 1000)
	d := mustWrite(t, s, 100)
	wantSent(t, d, "0:100@a")

	d, err := s.SubflowFail("a")
	if err != nil {
		t.Fatalf("SubflowFail: %v", err)
	}
	wantSent(t, d)

	// a 恢复后成为唯一活跃子流，但重新注入段不得选它。
	d, err = s.SubflowRecover("a")
	if err != nil {
		t.Fatalf("SubflowRecover: %v", err)
	}
	wantSent(t, d)

	d, err = s.AddSubflow("b", 20, 1000, Normal)
	if err != nil {
		t.Fatalf("AddSubflow: %v", err)
	}
	wantSent(t, d, "0:100@b(R)")
}

// 连接级确认覆盖的待发重新注入段被丢弃；部分覆盖则整段保留。
func TestConnAckDropsPendingReinjection(t *testing.T) {
	s := newSched(t, 100)
	mustAdd(t, s, "a", 10, 1000, Normal)
	mustConnAck(t, s, 0, 1000)
	mustWrite(t, s, 200)

	d, err := s.SubflowFail("a")
	if err != nil {
		t.Fatalf("SubflowFail: %v", err)
	}
	wantSent(t, d) // 无活跃子流，重新注入段排队等待

	// 确认到 150：[0,100) 被完全覆盖而丢弃；[100,200) 部分覆盖，整段保留。
	d = mustConnAck(t, s, 150, 900)
	wantSent(t, d)

	d, err = s.AddSubflow("b", 10, 1000, Normal)
	if err != nil {
		t.Fatalf("AddSubflow: %v", err)
	}
	wantSent(t, d, "100:100@b(R)")
}

// 队首阻塞：队首段无候选子流时，后续段不得越过它先发。
func TestHeadOfLineBlocking(t *testing.T) {
	s := newSched(t, 100)
	mustAdd(t, s, "a", 10, 100, Normal)
	mustConnAck(t, s, 0, 10000)
	mustWrite(t, s, 100)

	d, err := s.SubflowFail("a")
	if err != nil {
		t.Fatalf("SubflowFail: %v", err)
	}
	wantSent(t, d)

	// 重新注入段阻塞期间，新数据不得越过。
	d = mustWrite(t, s, 200)
	wantSent(t, d)

	// b 的窗口只够重新注入段，新数据继续等待。
	d, err = s.AddSubflow("b", 10, 100, Normal)
	if err != nil {
		t.Fatalf("AddSubflow: %v", err)
	}
	wantSent(t, d, "0:100@b(R)")

	// b 确认释放后，新数据按序发出（b 的窗口每次只够一段）。
	d, err = s.SubflowAck("b", 100)
	if err != nil {
		t.Fatalf("SubflowAck: %v", err)
	}
	wantSent(t, d, "100:100@b")
	d, err = s.SubflowAck("b", 200)
	if err != nil {
		t.Fatalf("SubflowAck: %v", err)
	}
	wantSent(t, d, "200:100@b")
}

// 重新注入段不受连接级窗口约束，新数据段仍受约束。
func TestReinjectIgnoresConnWindow(t *testing.T) {
	s := newSched(t, 100)
	mustAdd(t, s, "a", 5, 1000, Normal)
	mustAdd(t, s, "b", 10, 1000, Normal)
	mustConnAck(t, s, 0, 100)
	d := mustWrite(t, s, 100)
	wantSent(t, d, "0:100@a")

	// 窗口已用尽（上限 100），但重新注入段豁免。
	d, err := s.SubflowFail("a")
	if err != nil {
		t.Fatalf("SubflowFail: %v", err)
	}
	wantSent(t, d, "0:100@b(R)")

	// 新数据仍受窗口限制。
	d = mustWrite(t, s, 100)
	wantSent(t, d)
}

// 连接级确认不释放子流在途：重新注入副本在连接级确认覆盖后
// 仍占用所在子流的拥塞窗口，直到该子流确认释放。
func TestConnAckDoesNotReleaseSubflowInflight(t *testing.T) {
	s := newSched(t, 100)
	mustAdd(t, s, "a", 5, 1000, Normal)
	mustAdd(t, s, "b", 10, 1000, Normal)
	mustConnAck(t, s, 0, 1000)
	mustWrite(t, s, 100)

	d, err := s.SubflowFail("a")
	if err != nil {
		t.Fatalf("SubflowFail: %v", err)
	}
	wantSent(t, d, "0:100@b(R)")

	// 连接级确认覆盖该段：b 的在途不释放。
	d = mustConnAck(t, s, 100, 900)
	wantSent(t, d)
	if got := subflowOf(s.Snapshot(), "b").InflightBytes; got != 100 {
		t.Fatalf("连接级确认不得释放子流在途，实际在途 %d", got)
	}

	// 子流确认才释放。
	d, err = s.SubflowAck("b", 100)
	if err != nil {
		t.Fatalf("SubflowAck: %v", err)
	}
	wantSent(t, d)
	if got := subflowOf(s.Snapshot(), "b").InflightBytes; got != 0 {
		t.Fatalf("子流确认应释放子流在途，实际在途 %d", got)
	}
}

// 重复副本的记账规则（白盒）：同一连接级序号在两个子流的在途中
// 各有一份副本时，连接级确认不释放任何一份，各子流确认只释放自己的。
// 注：在纯事件驱动下该状态不可达（见 DESIGN.md 的证明），这里直接
// 构造内部状态，验证记账规则与规范一致。
func TestDuplicateCopiesAccounting(t *testing.T) {
	s := newSched(t, 100)
	mustAdd(t, s, "a", 5, 1000, Normal)
	mustAdd(t, s, "b", 10, 1000, Normal)
	mustConnAck(t, s, 0, 1000)

	// 直接构造：序号 [0,100) 同时在 a 与 b 的在途中。
	s.mu.Lock()
	for _, id := range []string{"a", "b"} {
		sub := s.subs[id]
		sub.inflight.push(seg{seq: 0, len: 100})
		sub.inflightBytes = 100
		sub.sentBytes = 100
	}
	s.sentMax = 100
	s.nextSeq = 100
	s.mu.Unlock()

	// 连接级确认覆盖：两份副本均不释放。
	mustConnAck(t, s, 100, 900)
	st := s.Snapshot()
	if subflowOf(st, "a").InflightBytes != 100 || subflowOf(st, "b").InflightBytes != 100 {
		t.Fatalf("连接级确认不得释放子流在途：%+v", st)
	}

	// 各子流确认只释放自己的副本。
	if _, err := s.SubflowAck("a", 100); err != nil {
		t.Fatalf("SubflowAck: %v", err)
	}
	st = s.Snapshot()
	if got := subflowOf(st, "a").InflightBytes; got != 0 {
		t.Fatalf("a 的在途应释放，实际 %d", got)
	}
	if got := subflowOf(st, "b").InflightBytes; got != 100 {
		t.Fatalf("b 的在途不应被 a 的确认释放，实际 %d", got)
	}
	if _, err := s.SubflowAck("b", 100); err != nil {
		t.Fatalf("SubflowAck: %v", err)
	}
	if got := subflowOf(s.Snapshot(), "b").InflightBytes; got != 0 {
		t.Fatalf("b 的在途应释放，实际 %d", got)
	}
}

// 子流恢复后拥塞窗口重置为初始值、在途为零。
func TestRecoverResetsCwnd(t *testing.T) {
	s := newSched(t, 100)
	mustAdd(t, s, "a", 10, 100, Normal)
	mustConnAck(t, s, 0, 10000)
	mustWrite(t, s, 100)
	if _, err := s.SubflowFail("a"); err != nil {
		t.Fatalf("SubflowFail: %v", err)
	}
	// 失效后重新注入段被 b 接走前，a 恢复：在途为零、窗口复原。
	if _, err := s.SubflowRecover("a"); err != nil {
		t.Fatalf("SubflowRecover: %v", err)
	}
	st := s.Snapshot()
	sub := subflowOf(st, "a")
	if !sub.Active || sub.Cwnd != 100 || sub.InflightBytes != 0 {
		t.Fatalf("恢复后状态不符：%+v", sub)
	}
	// 重新注入段 [0,100) 仍排除 a 且排在队首，新数据被队首阻塞。
	d := mustWrite(t, s, 100)
	wantSent(t, d)
	// 连接级确认覆盖后，待发重新注入段被丢弃，a 即可承接新数据。
	d = mustConnAck(t, s, 100, 10000)
	wantSent(t, d, "100:100@a")
}
