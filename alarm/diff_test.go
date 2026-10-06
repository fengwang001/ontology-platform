package alarm_test

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"

	"ontology/alarm"
)

// 朴素模型：独立于生产实现。每个被接受的操作都做：
//  1. 处理全部到期（until <= ts）；
//  2. 快照操作前后所有点的可见性，净 “false->true” 即一次新出现；
//  3. 全表扫描得到活动列表；报警率直接统计出现时刻。
// 不使用树/堆/任何增量结构，保证与生产代码实现思路相互独立。

type nPoint struct {
	pri   int
	conds []string

	state    string // normal / AU / AA / RU
	disabled bool

	shelved bool
	manual  bool
	until   int
	reason  string

	lastActive int
	chat       []int
	suppress   bool
}

type naiveSvc struct {
	cfg      alarm.Config
	points   map[int]*nPoint
	lastTS   int
	conds    map[string]bool
	appearAt []int
	expiries []int
}

func newNaive(cfg alarm.Config, pcs []alarm.PointConfig) *naiveSvc {
	n := &naiveSvc{cfg: cfg, points: map[int]*nPoint{}, conds: map[string]bool{}}
	for _, c := range pcs {
		n.points[c.ID] = &nPoint{pri: int(c.Priority), conds: append([]string(nil), c.SuppressConditions...), state: "normal"}
	}
	return n
}

func (n *naiveSvc) visible(p *nPoint) bool {
	if p.disabled || p.shelved || p.suppress {
		return false
	}
	return p.state == "AU" || p.state == "AA" || p.state == "RU"
}

func (n *naiveSvc) snapshot() map[int]bool {
	m := make(map[int]bool, len(n.points))
	for id, p := range n.points {
		m[id] = n.visible(p)
	}
	return m
}

// diffAppear 记录 before->after 中由不可见变为可见的点（同一操作每点至多一次）。
func (n *naiveSvc) diffAppear(before map[int]bool, at int) {
	for id, p := range n.points {
		if !before[id] && n.visible(p) {
			before[id] = true
			n.appearAt = append(n.appearAt, at)
		}
	}
}

type testOp struct {
	kind   string // trigger return ack shelve unshelve disable enable conditions list rate
	ts     int
	id     int
	role   int
	dur    int
	reason string
	ticket string
	conds  []string
}

func (n *naiveSvc) apply(op testOp) string {
	if op.ts < 0 {
		return "invalid-argument"
	}
	switch op.kind {
	case "shelve":
		if op.dur <= 0 || op.reason == "" || (op.role != 1 && op.role != 2) {
			return "invalid-argument"
		}
	case "ack":
		if op.role != 1 && op.role != 2 {
			return "invalid-argument"
		}
	case "disable", "enable":
		if op.ticket == "" || (op.role != 1 && op.role != 2) {
			return "invalid-argument"
		}
	case "rate":
		if op.dur <= 0 {
			return "invalid-argument"
		}
	}
	if op.ts < n.lastTS {
		return "clock-rollback"
	}

	var p *nPoint
	if op.kind != "conditions" && op.kind != "list" && op.kind != "rate" {
		var ok bool
		p, ok = n.points[op.id]
		if !ok {
			return "no-such-point"
		}
	}
	if (op.kind == "disable" || op.kind == "enable") && op.role != 2 {
		return "no-permission"
	}

	before := n.snapshot()

	// runExpiry 处理 until <= op.ts 的到期；出现记在真实到期时刻。
	runExpiry := func() {
		remaining := n.expiries[:0]
		for _, u := range n.expiries {
			if u > op.ts {
				remaining = append(remaining, u)
				continue
			}
			for _, q := range n.points {
				if q.shelved && q.until == u {
					q.shelved, q.manual, q.reason, q.until = false, false, "", 0
				}
			}
			n.diffAppear(before, u)
		}
		n.expiries = remaining
		n.lastTS = op.ts
	}

	// shelve：到期处理放在所有校验之后（状态/时长检查先于到期副作用）。
	if op.kind == "shelve" {
		if p.pri == int(alarm.PriorityEmergency) || p.disabled || p.shelved {
			return "state-not-allowed"
		}
		limit := n.cfg.HighShelveMaxSec
		if p.pri == int(alarm.PriorityLow) {
			limit = n.cfg.LowShelveMaxSec
		}
		if op.dur > limit {
			return "shelve-too-long"
		}
		runExpiry()
		// 到期不可能在同一时刻解除刚通过校验的 shelve（尚未登记），无需复查。
	} else {
		// 其他操作：先处理到期，再做状态检查。
		runExpiry()
		switch op.kind {
		case "unshelve":
			if !p.shelved {
				return "state-not-allowed"
			}
		case "ack":
			if p.state != "AU" && p.state != "RU" {
				return "state-not-allowed"
			}
		case "disable":
			if p.disabled {
				return "state-not-allowed"
			}
		case "enable":
			if !p.disabled {
				return "state-not-allowed"
			}
		}
	}

	switch op.kind {
	case "trigger":
		if !p.disabled && (p.state == "normal" || p.state == "RU") {
			p.state = "AU"
			p.lastActive = op.ts
			if !p.shelved {
				p.chat = append(p.chat, op.ts)
				cut := op.ts - n.cfg.ChatWindowSec
				kept := p.chat[:0]
				for _, t := range p.chat {
					if t > cut {
						kept = append(kept, t)
					}
				}
				p.chat = kept
				if p.pri != int(alarm.PriorityEmergency) && len(p.chat) >= n.cfg.ChatCount {
					p.chat = p.chat[:0]
					p.shelved, p.manual, p.until, p.reason = true, false, op.ts+n.cfg.ChatShelveSec, alarm.ChatReason
					n.expiries = append(n.expiries, p.until)
				}
			}
		}
	case "return":
		if !p.disabled {
			switch p.state {
			case "AU":
				p.state = "RU"
			case "AA":
				p.state = "normal"
			}
		}
	case "ack":
		switch p.state {
		case "AU":
			p.state = "AA"
		case "RU":
			p.state = "normal"
		}
	case "shelve":
		p.shelved, p.manual, p.until, p.reason = true, true, op.ts+op.dur, op.reason
		n.expiries = append(n.expiries, p.until)
	case "unshelve":
		p.shelved, p.manual, p.reason, p.until = false, false, "", 0
	case "disable":
		p.disabled = true
	case "enable":
		p.disabled = false
		p.state = "normal"
		p.lastActive = 0
		p.chat = p.chat[:0]
	case "conditions":
		next := map[string]bool{}
		for _, c := range op.conds {
			next[c] = true
		}
		n.conds = next
		for _, q := range n.points {
			q.suppress = false
			for _, c := range q.conds {
				if next[c] {
					q.suppress = true
					break
				}
			}
		}
	}

	n.diffAppear(before, op.ts)
	return ""
}

type nListRow struct {
	id, pri, tier, lastActive int
	state                     string
}

func (n *naiveSvc) list(ts int) []nListRow {
	_ = n.apply(testOp{kind: "list", ts: ts})
	var rows []nListRow
	for id, p := range n.points {
		if !n.visible(p) {
			continue
		}
		tier := 0
		if p.state == "AU" || p.state == "RU" {
			tier = 1
		}
		rows = append(rows, nListRow{id: id, pri: p.pri, tier: tier, lastActive: p.lastActive, state: p.state})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.pri != b.pri {
			return a.pri > b.pri
		}
		if a.tier != b.tier {
			return a.tier > b.tier
		}
		if a.lastActive != b.lastActive {
			return a.lastActive < b.lastActive
		}
		return a.id < b.id
	})
	return rows
}

func (n *naiveSvc) rate(ts, dur int) int {
	if code := n.apply(testOp{kind: "rate", ts: ts, dur: dur}); code != "" {
		return -1
	}
	lo := ts - dur
	cnt := 0
	for _, t := range n.appearAt {
		if t > lo && t <= ts {
			cnt++
		}
	}
	return cnt
}

func errCode(e error) string {
	if e == nil {
		return ""
	}
	if ae, ok := e.(*alarm.AlarmError); ok {
		switch ae.Code {
		case alarm.ErrInvalidArg:
			return "invalid-argument"
		case alarm.ErrClockRollback:
			return "clock-rollback"
		case alarm.ErrNoSuchPoint:
			return "no-such-point"
		case alarm.ErrNoPermission:
			return "no-permission"
		case alarm.ErrStateNotAllowed:
			return "state-not-allowed"
		case alarm.ErrShelveTooLong:
			return "shelve-too-long"
		}
	}
	return "other"
}

func stateCode(s string) alarm.State {
	switch s {
	case "AU":
		return alarm.StateActiveUnacked
	case "AA":
		return alarm.StateActiveAcked
	case "RU":
		return alarm.StateReturnUnacked
	default:
		return alarm.StateNormal
	}
}

var _ = fmt.Sprintf

// TestRandomDifferential 用随机事件序列逐步对照生产实现与朴素模型：
// 错误码、每点（状态/屏蔽/停用/抑制/最近激活时刻）、活动列表、报警率。
func TestRandomDifferential(t *testing.T) {
	cfg := testCfg()
	pointConfs := []alarm.PointConfig{
		{ID: 1, Priority: alarm.PriorityEmergency, SuppressConditions: []string{"C1"}},
		{ID: 2, Priority: alarm.PriorityHigh, SuppressConditions: []string{"C1", "C2"}},
		{ID: 3, Priority: alarm.PriorityLow},
		{ID: 4, Priority: alarm.PriorityHigh, SuppressConditions: []string{"C2"}},
	}

	for seed := int64(1); seed <= 60; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed*7919+1)))
			lg := testLogger(t)
			s := alarm.New(cfg, pointConfs, lg)
			n := newNaive(cfg, pointConfs)
			now := 0
			ids := []int{1, 2, 3, 4, 99}

			compare := func(ts int, opDesc string) {
				t.Helper()
				// 关键：生产与朴素模型执行完全相同顺序、相同时刻的附加操作，
				// 否则“惰性到期处理”被触发的次序不同会造成夹具自身的状态错位。
				for _, c := range pointConfs {
					id := c.ID
					sh, st, sup, err := s.ShelvingOf(ts, id)
					if err != nil {
						t.Fatalf("%s: ShelvingOf(%d): %v", opDesc, id, err)
					}
					_ = n.apply(testOp{kind: "list", ts: ts}) // 与 ShelvingOf 等价地推进朴素模型时钟/到期
					np := n.points[id]
					wantState := stateCode(np.state)
					if st != wantState {
						t.Fatalf("%s point %d state: got %v want %v", opDesc, id, st, wantState)
					}
					if sh.Active != np.shelved || sh.Manual != np.manual {
						t.Fatalf("%s point %d shelve: got %+v want shelved=%v manual=%v", opDesc, id, sh, np.shelved, np.manual)
					}
					if sh.Active && (sh.Until != np.until || sh.Reason != np.reason) {
						t.Fatalf("%s point %d shelve detail: got (until=%d reason=%q) want (until=%d reason=%q)",
							opDesc, id, sh.Until, sh.Reason, np.until, np.reason)
					}
					if sup != np.suppress {
						t.Fatalf("%s point %d suppress: got %v want %v", opDesc, id, sup, np.suppress)
					}
				}

				gotList, err := s.ActiveList(ts)
				if err != nil {
					t.Fatalf("%s ActiveList: %v", opDesc, err)
				}
				wantRows := n.list(ts)
				if len(gotList) != len(wantRows) {
					t.Fatalf("%s list len: got %d want %d (%v vs %v)", opDesc, len(gotList), len(wantRows), gotList, wantRows)
				}
				for i, r := range wantRows {
					g := gotList[i]
					if g.ID != r.id || int(g.Priority) != r.pri || g.State != stateCode(r.state) || g.LastActiveAt != r.lastActive {
						t.Fatalf("%s list row %d: got %+v want %+v", opDesc, i, g, r)
					}
				}

				dur := 1 + rng.IntN(15)
				gotRate, err := s.AlarmRate(ts, dur)
				if err != nil {
					t.Fatalf("%s AlarmRate: %v", opDesc, err)
				}
				wantRate := n.rate(ts, dur)
				if gotRate != wantRate {
					t.Fatalf("%s rate (ts=%d,dur=%d): got %d want %d", opDesc, ts, dur, gotRate, wantRate)
				}
			}

			for step := 0; step < 500; step++ {
				// 主随机流保持时刻非降（回退场景另有专项测试，
				// 因为被拒绝的回退不推进时钟，无法与后续时刻自动衔接）。
				ts := now + rng.IntN(3)
				now = ts

				op := testOp{ts: ts, id: ids[rng.IntN(len(ids))]}
				var prodErr error

				switch rng.IntN(10) {
				case 0:
					op.kind = "trigger"
					prodErr = s.Trigger(ts, op.id)
				case 1:
					op.kind = "return"
					prodErr = s.Return(ts, op.id)
				case 2:
					op.kind = "ack"
					op.role = 1 + rng.IntN(2)
					prodErr = s.Ack(ts, op.id, alarm.Role(op.role), "op")
				case 3:
					op.kind = "shelve"
					op.role = 1 + rng.IntN(2)
					op.dur = 1 + rng.IntN(120)
					op.reason = "maintenance"
					if rng.IntN(9) == 0 {
						op.dur = 0
					}
					if rng.IntN(9) == 0 {
						op.reason = ""
					}
					prodErr = s.Shelve(ts, op.id, alarm.Role(op.role), op.dur, op.reason)
				case 4:
					op.kind = "unshelve"
					op.role = 1 + rng.IntN(2)
					prodErr = s.Unshelve(ts, op.id, alarm.Role(op.role))
				case 5:
					op.kind = "disable"
					op.role = 1 + rng.IntN(2)
					op.ticket = "CHG"
					if rng.IntN(9) == 0 {
						op.ticket = ""
					}
					prodErr = s.Disable(ts, op.id, alarm.Role(op.role), op.ticket)
				case 6:
					op.kind = "enable"
					op.role = 1 + rng.IntN(2)
					op.ticket = "CHG"
					if rng.IntN(9) == 0 {
						op.ticket = ""
					}
					prodErr = s.Enable(ts, op.id, alarm.Role(op.role), op.ticket)
				case 7:
					op.kind = "conditions"
					var cs []string
					if rng.IntN(2) == 0 {
						cs = append(cs, "C1")
					}
					if rng.IntN(2) == 0 {
						cs = append(cs, "C2")
					}
					op.conds = cs
					prodErr = s.SetActiveConditions(ts, cs)
				case 8:
					op.kind = "list"
					op.ts = ts
					_, prodErr = s.ActiveList(ts)
				default:
					op.kind = "rate"
					op.dur = 1 + rng.IntN(15)
					if rng.IntN(12) == 0 {
						op.dur = 0
					}
					_, prodErr = s.AlarmRate(ts, op.dur)
				}

				wantCode := n.apply(op)
				gotCode := errCode(prodErr)
				opDesc := fmt.Sprintf("seed=%d step=%d op=%+v", seed, step, op)
				if gotCode != wantCode {
					t.Fatalf("%s: error code got %q want %q", opDesc, gotCode, wantCode)
				}

				// 被拒绝的操作不得改变状态：只有时钟已推进的查询时刻才做全量比对。
				if wantCode == "" && op.ts >= 0 {
					compare(ts, opDesc)
				}
			}
		})
	}
}
