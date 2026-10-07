package room_test

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"ontology/room"
)

// opKind 为随机序列中的操作种类。
type opKind int

const (
	opJoin opKind = iota
	opReady
	opUnready
	opLeave
	opEnd
	opReport
)

type op struct {
	kind         opKind
	user, winner string
	now          int64
}

func (o op) String() string {
	name := []string{"Join", "SetReady(true)", "SetReady(false)", "Leave", "End", "Report"}[o.kind]
	if o.kind == opReport {
		return fmt.Sprintf("%s(user=%q, winner=%q, now=%d)", name, o.user, o.winner, o.now)
	}
	return fmt.Sprintf("%s(user=%q, now=%d)", name, o.user, o.now)
}

func applyReal(r *room.Room, o op) error {
	switch o.kind {
	case opJoin:
		return r.Join(o.user, o.now)
	case opReady:
		return r.SetReady(o.user, true, o.now)
	case opUnready:
		return r.SetReady(o.user, false, o.now)
	case opLeave:
		return r.Leave(o.user, o.now)
	case opEnd:
		return r.End(o.user, o.now)
	case opReport:
		return r.Report(o.user, o.winner, o.now)
	}
	panic("bad op")
}

func applyNaive(n *naiveRoom, o op) error {
	switch o.kind {
	case opJoin:
		return n.join(o.user, o.now)
	case opReady:
		return n.setReady(o.user, true, o.now)
	case opUnready:
		return n.setReady(o.user, false, o.now)
	case opLeave:
		return n.leave(o.user, o.now)
	case opEnd:
		return n.end(o.user, o.now)
	case opReport:
		return n.report(o.user, o.winner, o.now)
	}
	panic("bad op")
}

func codeOf(err error) room.ErrCode {
	if err == nil {
		return room.ErrCodeNone
	}
	if re, ok := err.(*room.Error); ok {
		return re.Code
	}
	return -1
}

// genOps 生成一条随机操作序列：now 大体单调前进，偶尔回退以测试时钟回退。
func genOps(rng *rand.Rand, n int, users []string) []op {
	ops := make([]op, 0, n)
	var now int64
	for i := 0; i < n; i++ {
		// 4% 概率时钟回退。
		if rng.Uint64()%100 < 4 && now >= 2 {
			now -= 1 + int64(rng.Uint64()%2)
		} else {
			now += int64(rng.Uint64() % 4)
		}
		user := users[rng.Uint64()%uint64(len(users))]
		if rng.Uint64()%100 < 2 {
			user = "" // 2% 概率非法参数
		}
		var kind opKind
		switch x := rng.Uint64() % 100; {
		case x < 24:
			kind = opJoin
		case x < 44:
			kind = opReady
		case x < 54:
			kind = opUnready
		case x < 72:
			kind = opLeave
		case x < 80:
			kind = opEnd
		default:
			kind = opReport
		}
		o := op{kind: kind, user: user, now: now}
		if kind == opReport {
			o.winner = users[rng.Uint64()%uint64(len(users))]
			if rng.Uint64()%100 < 2 {
				o.winner = ""
			}
		}
		ops = append(ops, o)
	}
	return ops
}

func randomConfig(rng *rand.Rand) room.Config {
	l := int64(2 + rng.Uint64()%3) // 2..4
	u := l + int64(rng.Uint64()%4) // L..L+3，容易触发已满
	return room.Config{
		MinPlayers:   l,
		MaxPlayers:   u,
		Countdown:    1 + int64(rng.Uint64()%4), // 1..4
		ReportWindow: 1 + int64(rng.Uint64()%3), // 1..3
	}
}

// TestDifferentialAgainstNaive 用至少 1500 组随机操作序列对照
// 被测实现与独立朴素模型：逐步比较拒绝码与快照。
func TestDifferentialAgainstNaive(t *testing.T) {
	const sequences = 1500
	users := []string{"p0", "p1", "p2", "p3", "p4", "p5", "p6", "p7"}

	stateSeen := map[room.State]int{}
	codeSeen := map[room.ErrCode]int{}
	reasonSeen := map[room.AbortReason]int{}

	for s := 0; s < sequences; s++ {
		rng := rand.New(rand.NewPCG(uint64(s)+1, 0x9e3779b9))
		cfg := randomConfig(rng)
		ops := genOps(rng, 40, users)

		real, err := room.NewRoom(cfg)
		if err != nil {
			t.Fatalf("seq %d: NewRoom: %v", s, err)
		}
		naive := newNaive(cfg)

		var curNow int64
		var outcomes []string
		step := func(tag string, o op) {
			t.Helper()
			errR := applyReal(real, o)
			errN := applyNaive(naive, o)
			cr, cn := codeOf(errR), codeOf(errN)
			outcomes = append(outcomes, fmt.Sprintf("  %s %s -> real=%s naive=%s", tag, o, cr, cn))
			if cr != cn {
				t.Fatalf("seq %d %s %s: error code mismatch real=%s naive=%s\nconfig=%+v\nsequence:\n%s",
					s, tag, o, cr, cn, cfg, strings.Join(outcomes, "\n"))
			}
			codeSeen[cr]++
			// 通过参数与时钟检查的操作会推进时钟。
			if cr != room.ErrCodeInvalidParam && cr != room.ErrCodeClockRollback && o.now > curNow {
				curNow = o.now
			}
			snapR, qeR := real.Query(curNow)
			snapN, qeN := naive.query(curNow)
			if (qeR == nil) != (qeN == nil) {
				t.Fatalf("seq %d %s: query error mismatch: %v vs %v", s, tag, qeR, qeN)
			}
			if qeR != nil {
				return
			}
			if !reflect.DeepEqual(snapR, snapN) {
				t.Logf("seq %d %s %s: snapshot mismatch", s, tag, o)
				t.Logf("config: %+v", cfg)
				t.Logf("sequence:\n%s", strings.Join(outcomes, "\n"))
				t.Logf("real : %+v", snapR)
				t.Logf("naive: %+v", snapN)
				t.FailNow()
			}
			stateSeen[snapR.State]++
			if snapR.State == room.StateAborted {
				reasonSeen[snapR.AbortReason]++
			}
		}

		for i, o := range ops {
			step(fmt.Sprintf("op[%d]", i), o)
		}

		// 状态驱动收尾段：把房间驱动到终态，覆盖一致结束 / 争议 / 超时。
		// 三种模式轮转：0=全体一致，1=全体上报但争议，2=部分上报后跨过期限。
		mode := s % 3
		for i := 0; i < 64; i++ {
			snap, err := real.Query(curNow)
			if err != nil {
				t.Fatalf("seq %d tail query: %v", s, err)
			}
			switch snap.State {
			case room.StateWaiting:
				if int64(len(snap.Players)) < snap.Config.MinPlayers {
					curNow++
					step("tail-join", op{kind: opJoin, user: fmt.Sprintf("t%d", i), now: curNow})
					continue
				}
				needReady := ""
				for _, p := range snap.Players {
					if !p.Ready {
						needReady = p.ID
						break
					}
				}
				if needReady == "" {
					t.Fatalf("seq %d tail: waiting but all ready?", s)
				}
				curNow++
				step("tail-ready", op{kind: opReady, user: needReady, now: curNow})
			case room.StateCountdown:
				curNow = snap.CountdownExpiry // 推进到恰等于到期时刻
			case room.StateInProgress:
				curNow++
				step("tail-end", op{kind: opEnd, user: snap.Host, now: curNow})
			case room.StateSettling:
				reported := map[string]bool{}
				for _, p := range snap.Roster {
					reported[p.ID] = p.Reported
				}
				next := ""
				for _, p := range snap.Roster {
					if p.InRoom && !reported[p.ID] {
						next = p.ID
						break
					}
				}
				if next == "" {
					t.Fatalf("seq %d tail: settling but all reported?", s)
				}
				curNow++
				winner := snap.Roster[0].ID
				if mode == 1 && len(snap.Roster) > 1 && next == snap.Roster[0].ID {
					winner = snap.Roster[1].ID // 制造争议
				}
				if mode == 2 && snap.ReportCount > 0 {
					curNow = snap.ReportDeadline // 直接跨过期限触发超时裁决
					continue
				}
				step("tail-report", op{kind: opReport, user: next, winner: winner, now: curNow})
			default: // 终态
				i = 64
			}
		}
		if s == 0 {
			t.Logf("样例序列 seed=%d config=%+v", s, cfg)
			for _, line := range outcomes {
				t.Logf("%s", line)
			}
		}
	}

	t.Logf("覆盖统计: states=%v codes=%v abortReasons=%v", stateSeen, codeSeen, reasonSeen)
	for _, st := range []room.State{room.StateCountdown, room.StateInProgress, room.StateSettling, room.StateEnded, room.StateAborted} {
		if stateSeen[st] == 0 {
			t.Fatalf("随机序列未覆盖状态 %s", st)
		}
	}
	for _, reason := range []room.AbortReason{room.AbortDispute, room.AbortTimeout} {
		if reasonSeen[reason] == 0 {
			t.Fatalf("随机序列未覆盖作废原因 %s", reason)
		}
	}
	for _, c := range []room.ErrCode{
		room.ErrCodeInvalidParam, room.ErrCodeClockRollback, room.ErrCodePhaseNotAllowed,
		room.ErrCodeNotInRoom, room.ErrCodeNotHost, room.ErrCodeAlreadyReady,
		room.ErrCodeRoomFull, room.ErrCodeAlreadyJoined, room.ErrCodeTerminated,
	} {
		if codeSeen[c] == 0 {
			t.Fatalf("随机序列未覆盖拒绝码 %s", c)
		}
	}
	t.Logf("判定依据: 1500 组随机序列下，被测实现与朴素模型逐步拒绝码与快照完全一致")
}
