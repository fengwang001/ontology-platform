package querier

import (
	"errors"
	"reflect"
	"testing"

	"ontology/member"
)

const g = uint32(0xE1000001)
const lg = uint32(0xE0000001)

func mustNew(t *testing.T, P int, ownIP uint32, qi, qri, rb, lmqi int64, fast []bool, flood bool, gmax, lp int) *Controller {
	t.Helper()
	c, err := New(P, ownIP, qi, qri, rb, lmqi, fast, flood, gmax, lp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func qtimes(qs []Query) []int64 {
	out := make([]int64, len(qs))
	for i, q := range qs {
		out[i] = q.Time
	}
	return out
}

func gqs(qs []Query) []int64 {
	var out []int64
	for _, q := range qs {
		if q.Kind == General {
			out = append(out, q.Time)
		}
	}
	return out
}

func sgqs(qs []Query) []Query {
	var out []Query
	for _, q := range qs {
		if q.Kind == GroupSpecific {
			out = append(out, q)
		}
	}
	return out
}

func wantErr(t *testing.T, name string, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: got %v, want %v", name, got, want)
	}
}

// TestSpecLeave 规范例一：Leave 只降不升、特定组查询计划与恰到期转发。
func TestSpecLeave(t *testing.T) {
	c := mustNew(t, 4, 5, 125, 10, 2, 1, []bool{false, false, false, false}, false, 100, 100)
	if to, err := c.Report(1, g, 0); err != nil || len(to) != 0 {
		t.Fatalf("Report1: to=%v err=%v", to, err)
	}
	if to, err := c.Report(2, g, 100); err != nil || len(to) != 0 {
		t.Fatalf("Report2: to=%v err=%v", to, err)
	}
	if err := c.Leave(1, g, 200); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	qs, err := c.Drain(201)
	if err != nil {
		t.Fatal(err)
	}
	got := sgqs(qs)
	if len(got) != 2 || got[0].Time != 200 || got[1].Time != 201 ||
		got[0].Group != g || got[0].Port != 1 {
		t.Fatalf("sg queries = %+v", got)
	}
	f201, err := c.Forward(g, 3, 201)
	if err != nil || !reflect.DeepEqual(f201, []int{1, 2}) {
		t.Fatalf("Forward@201 = %v, %v", f201, err)
	}
	f202, err := c.Forward(g, 3, 202)
	if err != nil || !reflect.DeepEqual(f202, []int{2}) {
		t.Fatalf("Forward@202 = %v, %v", f202, err)
	}

	// 变体 A：t=201 同刻 Report 刷新：201 的查询照发，exp=461。
	c2 := mustNew(t, 4, 5, 125, 10, 2, 1, []bool{false, false, false, false}, false, 100, 100)
	_, _ = c2.Report(1, g, 0)
	_, _ = c2.Report(2, g, 100)
	_ = c2.Leave(1, g, 200)
	if _, err := c2.Drain(200); err != nil { // 只取 200 一条
		t.Fatal(err)
	}
	if _, err := c2.Report(1, g, 201); err != nil {
		t.Fatal(err)
	}
	qs2, _ := c2.Drain(201)
	if got := sgqs(qs2); len(got) != 1 || got[0].Time != 201 {
		t.Fatalf("same-time sg = %+v", got)
	}
	if f, _ := c2.Forward(g, 3, 460); !reflect.DeepEqual(f, []int{1}) {
		t.Fatalf("exp=461 alive@460: %v", f)
	}
	if f, _ := c2.Forward(g, 2, 461); !reflect.DeepEqual(f, []int{}) {
		t.Fatalf("exp=461 gone@461: %v", f)
	}

	// 变体 B：Leave@259：min(260,261)=260，仅 259 发出，260 随到期取消。
	c3 := mustNew(t, 4, 5, 125, 10, 2, 1, []bool{false, false, false, false}, false, 100, 100)
	_, _ = c3.Report(1, g, 0)
	_ = c3.Leave(1, g, 259)
	qs3, _ := c3.Drain(300)
	got3 := sgqs(qs3)
	if len(got3) != 1 || got3[0].Time != 259 {
		t.Fatalf("leave259 sg = %+v", got3)
	}
}

// TestSpecElection 规范例二：选举、让位、恢复相位与路由器端口存续。
func TestSpecElection(t *testing.T) {
	c := mustNew(t, 4, 5, 125, 10, 2, 1, []bool{false, false, false, false}, false, 100, 100)
	_, _ = c.Report(1, g, 0)
	if err := c.Query(4, 3, 300); err != nil {
		t.Fatal(err)
	}
	if c.IsQuerier() {
		t.Fatal("should step down at 300")
	}
	if err := c.Query(4, 9, 400); err != nil {
		t.Fatal(err)
	}
	if c.IsQuerier() {
		t.Fatal("srcIP 9 不应恢复查询器")
	}
	to600, err := c.Report(1, g, 600)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(to600, []int{4}) {
		t.Fatalf("Report@600 -> %v, want [4]", to600)
	}
	to655, err := c.Report(1, g, 655)
	if err != nil {
		t.Fatal(err)
	}
	if len(to655) != 0 {
		t.Fatalf("Report@655 -> %v, want []", to655)
	}
	qs, err := c.Drain(700)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gqs(qs), []int64{0, 125, 250, 555, 680}) {
		t.Fatalf("general queries = %v", qtimes(qs))
	}
}

// TestMemberExpiry 成员恰到期：exp-1 存活，恰在 exp 已删。
func TestMemberExpiry(t *testing.T) {
	c := mustNew(t, 2, 5, 125, 10, 1, 1, []bool{false, false}, true, 10, 10) // GMI=135
	_, _ = c.Report(1, g, 0)
	if f, _ := c.Forward(g, 2, 134); !reflect.DeepEqual(f, []int{1}) {
		t.Fatalf("alive@134 = %v", f)
	}
	if f, _ := c.Forward(g, 1, 135); !reflect.DeepEqual(f, []int{2}) {
		t.Fatalf("expired@135 flood = %v", f)
	}
}

// TestLowerOnly 离组只降不升；重复 Leave 不重排。
func TestLowerOnly(t *testing.T) {
	c := mustNew(t, 2, 5, 125, 10, 2, 10, []bool{false, false}, false, 10, 10)
	_, _ = c.Report(1, g, 0) // exp=260
	if err := c.Leave(1, g, 200); err != nil {
		t.Fatal(err)
	}
	if err := c.Leave(1, g, 210); err != nil { // 已有未发完安排：整体忽略
		t.Fatal(err)
	}
	qs, _ := c.Drain(215)
	if got := sgqs(qs); len(got) != 2 || got[0].Time != 200 || got[1].Time != 210 {
		t.Fatalf("lower-only sg = %+v", got)
	}
	if f, _ := c.Forward(g, 2, 219); !reflect.DeepEqual(f, []int{1}) {
		t.Fatalf("alive@219 = %v", f)
	}
	if f, _ := c.Forward(g, 2, 220); !reflect.DeepEqual(f, []int{}) {
		t.Fatalf("gone@220 = %v", f)
	}
}

// TestRepeatLeaveNoReschedule 重复 Leave 绝不重新安排特定组查询。
func TestRepeatLeaveNoReschedule(t *testing.T) {
	c := mustNew(t, 2, 5, 125, 10, 2, 5, []bool{false, false}, false, 10, 10)
	_, _ = c.Report(1, g, 0)
	_ = c.Leave(1, g, 100)
	qs, _ := c.Drain(102)
	if got := sgqs(qs); len(got) != 1 || got[0].Time != 100 {
		t.Fatalf("first = %+v", got)
	}
	if err := c.Leave(1, g, 103); err != nil {
		t.Fatal(err)
	}
	qs, _ = c.Drain(200)
	got := sgqs(qs)
	if len(got) != 1 || got[0].Time != 105 {
		t.Fatalf("repeat rescheduled: %+v", got)
	}
}

// TestFastLeave 快速离组立即移除且不安排特定组查询。
func TestFastLeave(t *testing.T) {
	c := mustNew(t, 2, 5, 125, 10, 2, 1, []bool{true, false}, false, 10, 10)
	_, _ = c.Report(1, g, 0)
	if err := c.Leave(1, g, 50); err != nil {
		t.Fatal(err)
	}
	if f, _ := c.Forward(g, 2, 50); !reflect.DeepEqual(f, []int{}) {
		t.Fatalf("fast leave: %v", f)
	}
	qs, _ := c.Drain(50)
	if len(sgqs(qs)) != 0 {
		t.Fatalf("fast leave must not schedule: %+v", sgqs(qs))
	}
	wantErr(t, "leave again", c.Leave(1, g, 51), member.ErrNotMember)
}

// TestLeaveWhenNonQuerier 非查询器的 Leave 被接受但无效果。
func TestLeaveWhenNonQuerier(t *testing.T) {
	c := mustNew(t, 2, 5, 125, 10, 2, 1, []bool{false, false}, false, 10, 10)
	_, _ = c.Report(1, g, 0) // exp=260
	_ = c.Query(2, 3, 100)   // 让位，oq=355
	if err := c.Leave(1, g, 200); err != nil {
		t.Fatalf("non-querier leave must be accepted: %v", err)
	}
	if f, _ := c.Forward(g, 2, 260); !reflect.DeepEqual(f, []int{}) {
		t.Fatalf("exp unchanged, gone@260 got %v", f)
	}
}

// TestLargerSrcIPOnlyRouter 较大 srcIP 只刷新路由器端口，不影响选举与 oq。
func TestLargerSrcIPOnlyRouter(t *testing.T) {
	c := mustNew(t, 4, 5, 125, 10, 2, 1, []bool{false, false, false, false}, false, 10, 10)
	_ = c.Query(4, 3, 300)
	_ = c.Query(4, 9, 400)
	if c.IsQuerier() {
		t.Fatal("larger srcIP must not restore querier")
	}
	qs, _ := c.Drain(600)
	if !reflect.DeepEqual(gqs(qs), []int64{0, 125, 250, 555}) {
		t.Fatalf("oq must stay 555: %v", gqs(qs))
	}
	if to, _ := c.Report(1, g, 600); !reflect.DeepEqual(to, []int{4}) {
		t.Fatalf("router alive@600: %v", to)
	}
	if to, _ := c.Report(1, g, 655); len(to) != 0 {
		t.Fatalf("router gone@655: %v", to)
	}
}

// TestOQExactRecovery oq 恰到期恢复，相位从 oq 重置；同刻 Query 先恢复再让位。
func TestOQExactRecovery(t *testing.T) {
	// QI=10,QRI=2,Rb=2 => OQPI=21。
	c := mustNew(t, 2, 5, 10, 2, 2, 1, []bool{false, false}, false, 10, 10)
	_ = c.Query(2, 3, 5) // 让位，oq=26
	qs, _ := c.Drain(26)
	if !reflect.DeepEqual(gqs(qs), []int64{0, 26}) {
		t.Fatalf("exact recovery: %v", gqs(qs))
	}
	qs, _ = c.Drain(46)
	if !reflect.DeepEqual(gqs(qs), []int64{36, 46}) {
		t.Fatalf("phase reset: %v", gqs(qs))
	}
	_ = c.Query(2, 3, 46) // 同刻：先恢复发 46，再让位，oq=67
	qs, _ = c.Drain(67)
	if !reflect.DeepEqual(gqs(qs), []int64{67}) {
		t.Fatalf("same-time recover-then-stepdown: %v", gqs(qs))
	}
}

// TestStepdownCancelsSGQ 让位取消未发特定组查询，已降 exp 不恢复。
func TestStepdownCancelsSGQ(t *testing.T) {
	c := mustNew(t, 2, 5, 125, 10, 3, 10, []bool{false, false}, false, 10, 10)
	_, _ = c.Report(1, g, 0) // exp=385
	_ = c.Leave(1, g, 100)   // exp 降为 130，SGQ 100,110,120
	qs, _ := c.Drain(100)
	if len(sgqs(qs)) != 1 {
		t.Fatalf("sg@100 = %+v", sgqs(qs))
	}
	_ = c.Query(2, 3, 105) // 让位：110、120 取消，exp 保持 130
	qs, _ = c.Drain(200)
	if len(sgqs(qs)) != 0 {
		t.Fatalf("cancelled sg leaked: %+v", sgqs(qs))
	}
	// Drain(200) 已把时钟推到 200，成员 exp=130 早已到期；改为独立实例验证“exp 不恢复”。
	c2 := mustNew(t, 2, 5, 125, 10, 3, 10, []bool{false, false}, false, 10, 10)
	_, _ = c2.Report(1, g, 0)
	_ = c2.Leave(1, g, 100)
	_, _ = c2.Drain(100)
	_ = c2.Query(2, 3, 105)
	// 让位后仅 Drain 到 129：exp 已被降到 130，成员仍在。
	if f, _ := c2.Forward(g, 2, 129); !reflect.DeepEqual(f, []int{1}) {
		t.Fatalf("lowered exp kept, alive@129: %v", f)
	}
	if f, _ := c2.Forward(g, 2, 130); !reflect.DeepEqual(f, []int{}) {
		t.Fatalf("expired@130: %v", f)
	}
}

// TestLimitsOrder 端口超限先于组超限；到期者释放名额；拒绝不改状态。
func TestLimitsOrder(t *testing.T) {
	c := mustNew(t, 3, 5, 125, 10, 1, 1, []bool{false, false, false}, false, 2, 1)
	g1, g2, g3 := uint32(0xE1000001), uint32(0xE2000002), uint32(0xE3000003)
	if _, err := c.Report(1, g1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Report(2, g2, 0); err != nil {
		t.Fatal(err)
	}
	_, err := c.Report(1, g3, 0)
	wantErr(t, "port limit first", err, member.ErrPortLimit)
	_, err = c.Report(3, g3, 0)
	wantErr(t, "group limit", err, member.ErrGroupLimit)
	if _, err := c.Report(1, g3, 135); err != nil { // GMI=135，g1 到期
		t.Fatalf("expired frees both slots: %v", err)
	}
}

// TestForwardUnknownAndLocal 未知组两种策略与本地链路组泛洪。
func TestForwardUnknownAndLocal(t *testing.T) {
	noFlood := mustNew(t, 4, 5, 125, 10, 1, 1, []bool{false, false, false, false}, false, 10, 10)
	if f, _ := noFlood.Forward(g, 3, 0); !reflect.DeepEqual(f, []int{}) {
		t.Fatalf("unknown no-flood = %v", f)
	}
	flood := mustNew(t, 4, 5, 125, 10, 1, 1, []bool{false, false, false, false}, true, 10, 10)
	if f, _ := flood.Forward(g, 3, 0); !reflect.DeepEqual(f, []int{1, 2, 4}) {
		t.Fatalf("unknown flood = %v", f)
	}
	if f, _ := noFlood.Forward(lg, 1, 0); !reflect.DeepEqual(f, []int{2, 3, 4}) {
		t.Fatalf("link-local flood = %v", f)
	}
	_, _ = noFlood.Report(1, g, 0)
	_ = noFlood.Query(4, 3, 0)
	if f, _ := noFlood.Forward(g, 3, 0); !reflect.DeepEqual(f, []int{1, 4}) {
		t.Fatalf("member union router = %v", f)
	}
}

// TestReportCancelsRest Report 取消余下查询，同刻查询照发。
func TestReportCancelsRest(t *testing.T) {
	c := mustNew(t, 2, 5, 125, 10, 3, 10, []bool{false, false}, false, 10, 10)
	_, _ = c.Report(1, g, 0)
	_ = c.Leave(1, g, 100) // SGQ 100,110,120
	_, _ = c.Report(1, g, 110)
	qs, _ := c.Drain(200)
	got := sgqs(qs)
	if len(got) != 2 || got[0].Time != 100 || got[1].Time != 110 {
		t.Fatalf("100/110 sent, 120 cancelled: %+v", got)
	}
}

// TestRejections 拒绝次序与时钟推进不变性。
func TestRejections(t *testing.T) {
	t.Run("invalid params", func(t *testing.T) {
		cases := []struct {
			name string
			P    int
			ip   uint32
			qi   int64
			qri  int64
			rb   int64
			lmqi int64
			fl   []bool
		}{
			{"P=0", 0, 5, 1, 1, 1, 1, []bool{}},
			{"P=257", 257, 5, 1, 1, 1, 1, make([]bool, 257)},
			{"ownIP=0", 2, 0, 1, 1, 1, 1, []bool{false, false}},
			{"QI=0", 2, 5, 0, 1, 1, 1, []bool{false, false}},
			{"QRI=QI", 2, 5, 10, 10, 1, 1, []bool{false, false}},
			{"Rb=8", 2, 5, 10, 5, 8, 1, []bool{false, false}},
			{"LMQI=0", 2, 5, 10, 5, 1, 0, []bool{false, false}},
			{"fastLeave length", 2, 5, 10, 5, 1, 1, []bool{false}},
		}
		for _, tc := range cases {
			if _, err := New(tc.P, tc.ip, tc.qi, tc.qri, tc.rb, tc.lmqi, tc.fl, false, 1, 1); !errors.Is(err, member.ErrInvalidParam) {
				t.Fatalf("%s: %v", tc.name, err)
			}
		}
		c := mustNew(t, 2, 5, 125, 10, 1, 1, []bool{false, false}, false, 10, 10)
		if _, err := c.Report(0, g, 0); !errors.Is(err, member.ErrInvalidParam) {
			t.Fatalf("bad port: %v", err)
		}
		if _, err := c.Report(1, 0xDFFFFFFF, 0); !errors.Is(err, member.ErrInvalidParam) {
			t.Fatalf("bad group: %v", err)
		}
		if err := c.Query(1, 0, 0); !errors.Is(err, member.ErrInvalidParam) {
			t.Fatalf("src=0: %v", err)
		}
		if err := c.Query(1, 5, 0); !errors.Is(err, member.ErrInvalidParam) {
			t.Fatalf("src=own: %v", err)
		}
	})
	t.Run("clock rollback", func(t *testing.T) {
		c := mustNew(t, 2, 5, 125, 10, 1, 1, []bool{false, false}, false, 10, 10)
		_, _ = c.Report(1, g, 10)
		if _, err := c.Report(1, g, 9); !errors.Is(err, member.ErrClockRollback) {
			t.Fatalf("rollback report: %v", err)
		}
		if _, err := c.Drain(9); !errors.Is(err, member.ErrClockRollback) {
			t.Fatalf("rollback drain: %v", err)
		}
		// 被拒后时钟仍为 10，成员关系不变。
		if f, _ := c.Forward(g, 2, 10); !reflect.DeepEqual(f, []int{1}) {
			t.Fatalf("state after rejected op: %v", f)
		}
	})
	t.Run("local then notmember", func(t *testing.T) {
		c := mustNew(t, 2, 5, 125, 10, 1, 1, []bool{false, false}, false, 10, 10)
		_, err := c.Report(1, lg, 0)
		wantErr(t, "report local", err, member.ErrLocalGroup)
		err = c.Leave(1, lg, 0)
		wantErr(t, "leave local before notmember", err, member.ErrLocalGroup)
		err = c.Leave(1, g, 0)
		wantErr(t, "leave unknown", err, member.ErrNotMember)
	})
}

// TestTouchedBound touched 不超过本组成员数+路由器端口数+1，且与组总数无关。
func TestTouchedBound(t *testing.T) {
	for _, nGroups := range []int{10, 10000} {
		P := 8
		c := mustNew(t, P, 5, 1_000_000, 1000, 1, 1, make([]bool, P), false, nGroups+10, nGroups+10)
		groups := make([]uint32, nGroups)
		for i := range groups {
			groups[i] = uint32(0xE0000100) + uint32(i)
			if groups[i] > 0xEFFFFFFF {
				groups[i] = 0xE1000000 + uint32(i)%0x00FFFF00
			}
			if _, err := c.Report(1+(i%P), groups[i], 0); err != nil {
				t.Fatalf("seed group %d: %v", i, err)
			}
		}
		target := groups[0]
		// 让 target 有 3 个成员，另造 2 个路由器端口。
		_, _ = c.Report(2, target, 1)
		_, _ = c.Report(3, target, 1)
		_ = c.Query(4, 2, 1)
		_ = c.Query(5, 2, 1)
		if _, err := c.Forward(target, 8, 2); err != nil {
			t.Fatal(err)
		}
		touched := c.Touched()
		bound := 3 + 2 + 1
		if touched > bound {
			t.Fatalf("nGroups=%d touched=%d > bound=%d", nGroups, touched, bound)
		}
		if touched != 3 {
			t.Fatalf("nGroups=%d touched=%d, want 3 (only this group's members)", nGroups, touched)
		}
	}
}
