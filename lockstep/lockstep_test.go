package lockstep

import (
	"errors"
	"testing"

	"ontology/turn"
)

// op 是脚本化操作：kind 为 "sub" 或 "adv"，err 为期望的拒绝原因（nil 表示接受）。
type op struct {
	kind string
	now  int64
	p, k int
	cmd  string
	err  error
}

func sub(now int64, p, k int, cmd string, err error) op {
	return op{kind: "sub", now: now, p: p, k: k, cmd: cmd, err: err}
}

func adv(now int64, err error) op {
	return op{kind: "adv", now: now, err: err}
}

// ent 是期望的单玩家记录。
type ent struct {
	in  string
	src turn.Source
}

func real(in string) ent { return ent{in, turn.Real} }
func rep(in string) ent  { return ent{in, turn.Repeat} }
func empty() ent         { return ent{"", turn.Empty} }

// wantLog 是期望的回合记录。
type wantLog struct {
	turn int
	ts   int64
	ents []ent
}

func runOps(t *testing.T, e *Engine, ops []op) {
	t.Helper()
	for i, o := range ops {
		var err error
		switch o.kind {
		case "sub":
			err = e.Submit(o.now, o.p, o.k, []byte(o.cmd))
		case "adv":
			_, err = e.Advance(o.now)
		default:
			t.Fatalf("未知操作 %q", o.kind)
		}
		if !errors.Is(err, o.err) {
			t.Fatalf("op %d %+v: 错误=%v, 期望=%v", i, o, err, o.err)
		}
		t.Logf("op %d %+v -> err=%v（判定次序: 参数>时钟>入口处理>迟到>超前>重复）", i, o, err)
	}
}

func checkLogs(t *testing.T, got []turn.Log, want []wantLog) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("回合数=%d, 期望=%d; got=%v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Turn != w.turn || g.Ts != w.ts {
			t.Fatalf("第 %d 条: got (turn=%d, ts=%d), want (turn=%d, ts=%d)",
				i, g.Turn, g.Ts, w.turn, w.ts)
		}
		if len(g.Entries) != len(w.ents) {
			t.Fatalf("回合 %d: 记录数=%d, 期望=%d", w.turn, len(g.Entries), len(w.ents))
		}
		for p, we := range w.ents {
			ge := g.Entries[p]
			if ge.Src != we.src || string(ge.Input) != we.in {
				t.Errorf("回合 %d 玩家 %d: got (%q, %v), want (%q, %v)",
					w.turn, p, string(ge.Input), ge.Src, we.in, we.src)
			}
		}
	}
}

type scenario struct {
	name         string
	n            int
	timeout      int64
	ahead, r, kd int
	now0         int64
	ops          []op
	logs         []wantLog
	check        func(t *testing.T, e *Engine)
}

func TestScenarios(t *testing.T) {
	cases := []scenario{
		{
			name: "超时取等且结算时刻取dl",
			n:    1, timeout: 100, ahead: 0, r: 0, kd: 1,
			ops: []op{adv(99, nil), adv(100, nil), adv(300, nil)},
			logs: []wantLog{
				{1, 100, []ent{empty()}},
				{2, 200, []ent{empty()}},
				{3, 300, []ent{empty()}},
			},
		},
		{
			name: "一次Advance追赶多个回合",
			n:    2, timeout: 10, ahead: 0, r: 0, kd: 100,
			ops: []op{adv(55, nil)},
			logs: []wantLog{
				{1, 10, []ent{empty(), empty()}},
				{2, 20, []ent{empty(), empty()}},
				{3, 30, []ent{empty(), empty()}},
				{4, 40, []ent{empty(), empty()}},
				{5, 50, []ent{empty(), empty()}},
			},
		},
		{
			name: "提前提交引起同一ts连锁结算",
			n:    2, timeout: 1000, ahead: 3, r: 0, kd: 1,
			ops: []op{
				sub(0, 0, 1, "a", nil), sub(0, 0, 2, "b", nil), sub(0, 0, 3, "c", nil),
				sub(5, 1, 2, "x", nil), sub(5, 1, 3, "y", nil),
				sub(7, 1, 1, "z", nil),
			},
			logs: []wantLog{
				{1, 7, []ent{real("a"), real("z")}},
				{2, 7, []ent{real("b"), real("x")}},
				{3, 7, []ent{real("c"), real("y")}},
			},
			check: func(t *testing.T, e *Engine) {
				if e.cur != 4 || e.dl != 1007 {
					t.Errorf("连锁后 cur=%d dl=%d, 期望 cur=4 dl=1007", e.cur, e.dl)
				}
			},
		},
		{
			name: "m恰等于R时重复_R+1时空填",
			n:    1, timeout: 10, ahead: 0, r: 2, kd: 100,
			ops: []op{sub(0, 0, 1, "a", nil), adv(35, nil)},
			logs: []wantLog{
				{1, 0, []ent{real("a")}},
				{2, 10, []ent{rep("a")}},
				{3, 20, []ent{rep("a")}},
				{4, 30, []ent{empty()}},
			},
		},
		{
			name: "从未实交者只能空填",
			n:    2, timeout: 10, ahead: 0, r: 100, kd: 100,
			ops: []op{sub(0, 0, 1, "a", nil), adv(25, nil)},
			logs: []wantLog{
				{1, 10, []ent{real("a"), empty()}},
				{2, 20, []ent{rep("a"), empty()}},
			},
		},
		{
			name: "Kd小于等于R时掉线者仍得重复填充",
			n:    1, timeout: 10, ahead: 0, r: 2, kd: 1,
			ops: []op{sub(0, 0, 1, "a", nil), adv(35, nil)},
			logs: []wantLog{
				{1, 0, []ent{real("a")}},
				{2, 10, []ent{rep("a")}},
				{3, 20, []ent{rep("a")}},
				{4, 30, []ent{empty()}},
			},
			check: func(t *testing.T, e *Engine) {
				if e.players[0].Active {
					t.Errorf("m>=Kd 后玩家仍活跃")
				}
			},
		},
		{
			name: "全员非活跃时不因空集到齐",
			n:    2, timeout: 10, ahead: 2, r: 0, kd: 1,
			ops: []op{adv(35, nil), sub(35, 0, 4, "x", nil)},
			logs: []wantLog{
				{1, 10, []ent{empty(), empty()}},
				{2, 20, []ent{empty(), empty()}},
				{3, 30, []ent{empty(), empty()}},
				{4, 35, []ent{real("x"), empty()}},
			},
		},
		{
			name: "窗口两端取等",
			n:    2, timeout: 100, ahead: 2, r: 0, kd: 5,
			ops: []op{
				sub(0, 0, 1, "a", nil),
				sub(0, 0, 3, "c", nil),
				sub(0, 0, 4, "d", ErrAhead),
				sub(0, 1, 1, "b", nil),
				sub(0, 0, 1, "z", ErrLate),
				sub(0, 0, 4, "d", nil),
			},
			logs: []wantLog{
				{1, 0, []ent{real("a"), real("b")}},
			},
		},
		{
			name: "迟到判定发生在入口处理之后",
			n:    2, timeout: 100, ahead: 2, r: 1, kd: 3,
			ops: []op{
				sub(0, 0, 1, "a", nil),
				sub(0, 1, 1, "b", nil),
				sub(150, 0, 2, "c", ErrLate),
			},
			logs: []wantLog{
				{1, 0, []ent{real("a"), real("b")}},
				{2, 100, []ent{rep("a"), rep("b")}},
			},
		},
		{
			name: "拒绝次序",
			n:    2, timeout: 100, ahead: 2, r: 0, kd: 5,
			ops: []op{
				sub(-5, 9, 1, "x", ErrParam),
				sub(0, 0, 1, "a", nil),
				sub(0, 1, 1, "b", nil),
				adv(50, nil),
				sub(10, 0, 1, "z", ErrClock),
				sub(50, 0, 2, "x", nil),
				sub(50, 0, 2, "y", ErrDuplicate),
				adv(150, nil),
				sub(150, 0, 2, "w", ErrLate),
			},
			logs: []wantLog{
				{1, 0, []ent{real("a"), real("b")}},
				{2, 100, []ent{real("x"), empty()}},
			},
		},
		{
			name: "重复以首次为准",
			n:    1, timeout: 100, ahead: 1, r: 0, kd: 1,
			ops: []op{
				sub(0, 0, 2, "first", nil),
				sub(0, 0, 2, "second", ErrDuplicate),
				sub(0, 0, 1, "a", nil),
			},
			logs: []wantLog{
				{1, 0, []ent{real("a")}},
				{2, 0, []ent{real("first")}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, err := New(tc.n, tc.timeout, tc.ahead, tc.r, tc.kd, tc.now0)
			if err != nil {
				t.Fatalf("New 失败: %v", err)
			}
			t.Logf("参数: N=%d T=%d A=%d R=%d Kd=%d now0=%d",
				tc.n, tc.timeout, tc.ahead, tc.r, tc.kd, tc.now0)
			runOps(t, e, tc.ops)
			logs := e.Log(1)
			for _, lg := range logs {
				t.Logf("输出: 回合 %d ts=%d 记录=%v", lg.Turn, lg.Ts, lg.Entries)
			}
			checkLogs(t, logs, tc.logs)
			if tc.check != nil {
				tc.check(t, e)
			}
		})
	}
}

// TestWorkedExample 逐步复现题面示例（含续例），并断言内部状态。
func TestWorkedExample(t *testing.T) {
	e, err := New(2, 100, 2, 1, 3, 0)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("意外拒绝: %v", err)
		}
	}
	submit := func(now int64, p, k int, cmd string) error {
		err := e.Submit(now, p, k, []byte(cmd))
		t.Logf("Submit(now=%d, p=%d, k=%d, cmd=%q) -> %v", now, p, k, cmd, err)
		return err
	}
	advance := func(now int64) {
		logs, err := e.Advance(now)
		if err != nil {
			t.Fatalf("Advance(%d) 意外拒绝: %v", now, err)
		}
		for _, lg := range logs {
			t.Logf("Advance(%d) 结算: 回合 %d ts=%d 记录=%v", now, lg.Turn, lg.Ts, lg.Entries)
		}
	}
	active := func(p int) bool { return e.players[p].Active }
	miss := func(p int) int { return e.players[p].M }

	must(submit(10, 0, 1, "a"))
	must(submit(20, 1, 1, "b"))
	checkLogs(t, e.Log(1), []wantLog{{1, 20, []ent{real("a"), real("b")}}})
	if e.dl != 120 {
		t.Fatalf("dl=%d, 期望 120", e.dl)
	}

	must(submit(30, 0, 2, "c"))
	must(submit(40, 0, 3, "d"))
	if err := submit(125, 1, 2, "x"); !errors.Is(err, ErrLate) {
		t.Fatalf("期望迟到, got %v", err)
	}
	checkLogs(t, e.Log(2), []wantLog{{2, 120, []ent{real("c"), rep("b")}}})
	if miss(1) != 1 {
		t.Fatalf("p1 m=%d, 期望 1", miss(1))
	}

	advance(330)
	checkLogs(t, e.Log(3), []wantLog{
		{3, 220, []ent{real("d"), empty()}},
		{4, 320, []ent{rep("d"), empty()}},
	})
	if active(1) {
		t.Fatalf("p1 m=3>=Kd 应掉线")
	}
	if e.dl != 420 {
		t.Fatalf("dl=%d, 期望 420", e.dl)
	}

	must(submit(340, 0, 5, "e"))
	checkLogs(t, e.Log(5), []wantLog{{5, 340, []ent{real("e"), empty()}}})

	must(submit(350, 1, 7, "y"))
	if !active(1) || miss(1) != 4 {
		t.Fatalf("p1 恢复活跃但 m 应保持 4, got active=%v m=%d", active(1), miss(1))
	}
	must(submit(360, 0, 6, "f"))
	if len(e.Log(6)) != 0 {
		t.Fatalf("回合 6 不应到齐（p1 未交 6）")
	}

	advance(440)
	checkLogs(t, e.Log(6), []wantLog{{6, 440, []ent{real("f"), empty()}}})
	if active(1) {
		t.Fatalf("p1 应再次掉线")
	}

	must(submit(450, 0, 7, "g"))
	checkLogs(t, e.Log(7), []wantLog{{7, 450, []ent{real("g"), real("y")}}})
	if active(1) {
		t.Fatalf("p1 的存量输入按实交处理但不复活")
	}
	if miss(1) != 0 {
		t.Fatalf("p1 实交回合结算后 m 应归零, got %d", miss(1))
	}

	advance(550)
	checkLogs(t, e.Log(8), []wantLog{{8, 550, []ent{rep("g"), rep("y")}}})
	if miss(1) != 1 {
		t.Fatalf("p1 缺失回合 8 后 m 应为 1, got %d", miss(1))
	}
}

func TestNewValidation(t *testing.T) {
	good := func() bool { _, err := New(2, 100, 2, 1, 3, 0); return err == nil }
	if !good() {
		t.Fatal("合法参数被拒绝")
	}
	bad := []struct {
		name         string
		n            int
		timeout      int64
		ahead, r, kd int
		now0         int64
	}{
		{"N=0", 0, 100, 2, 1, 3, 0},
		{"N=4097", 4097, 100, 2, 1, 3, 0},
		{"T=0", 2, 0, 2, 1, 3, 0},
		{"T=1e6+1", 2, 1_000_001, 2, 1, 3, 0},
		{"A=-1", 2, 100, -1, 1, 3, 0},
		{"A=65", 2, 100, 65, 1, 3, 0},
		{"R=-1", 2, 100, 2, -1, 3, 0},
		{"R=101", 2, 100, 2, 101, 3, 0},
		{"Kd=0", 2, 100, 2, 1, 0, 0},
		{"Kd=101", 2, 100, 2, 1, 101, 0},
		{"now0=-1", 2, 100, 2, 1, 3, -1},
		{"now0=1e12+1", 2, 100, 2, 1, 3, 1_000_000_000_001},
	}
	for _, tc := range bad {
		if _, err := New(tc.n, tc.timeout, tc.ahead, tc.r, tc.kd, tc.now0); !errors.Is(err, ErrParam) {
			t.Errorf("%s: 期望 ErrParam, got %v", tc.name, err)
		}
	}
}
