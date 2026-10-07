package meeting_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/meeting"
	"ontology/meeting/naive"
)

// 本文件把优化实现 meeting.Room 与独立朴素模型 naive.Room 做随机差分对照：
// 至少 1500 组随机操作序列，逐步比较每个操作的拒绝类别/原因、
// 快照内容与队列名次，并打印输入、输出与判定依据。

type opKind int

const (
	opJoin opKind = iota
	opLeave
	opRaise
	opLower
	opAppoint
	opRevoke
	opGrant
	opYield
	opMute
	opUnmute
	opSnapshot
	opQueuePos
)

type op struct {
	kind           opKind
	user, operator string
	target         string
	now            int64
}

func (o op) String() string {
	switch o.kind {
	case opJoin:
		return fmt.Sprintf("Join(%q,%d)", o.user, o.now)
	case opLeave:
		return fmt.Sprintf("Leave(%q,%d)", o.user, o.now)
	case opRaise:
		return fmt.Sprintf("Raise(%q,%d)", o.user, o.now)
	case opLower:
		return fmt.Sprintf("Lower(%q,%d)", o.user, o.now)
	case opAppoint:
		return fmt.Sprintf("Appoint(%q,%q,%d)", o.operator, o.target, o.now)
	case opRevoke:
		return fmt.Sprintf("Revoke(%q,%q,%d)", o.operator, o.target, o.now)
	case opGrant:
		return fmt.Sprintf("Grant(%q,%d)", o.operator, o.now)
	case opYield:
		return fmt.Sprintf("Yield(%q,%d)", o.user, o.now)
	case opMute:
		return fmt.Sprintf("Mute(%q,%q,%d)", o.operator, o.target, o.now)
	case opUnmute:
		return fmt.Sprintf("Unmute(%q,%q,%d)", o.operator, o.target, o.now)
	case opSnapshot:
		return fmt.Sprintf("Snapshot(%d)", o.now)
	case opQueuePos:
		return fmt.Sprintf("QueuePos(%q)", o.user)
	}
	return "?"
}

// outcome 是一次操作的规范化结果，用于两个实现之间的精确比较。
type outcome struct {
	err    string // "ok" 或 "rej:<category>:<reason>"
	snap   *meeting.Snapshot
	pos    int
	hasPos bool
}

func errString(err error) string {
	if err == nil {
		return "ok"
	}
	if rj, ok := err.(*meeting.Reject); ok {
		return fmt.Sprintf("rej:%d:%s", rj.Cat, rj.Reason)
	}
	return "other:" + err.Error()
}

func applyOpt(r *meeting.Room, o op) outcome {
	switch o.kind {
	case opJoin:
		return outcome{err: errString(r.Join(o.user, o.now))}
	case opLeave:
		return outcome{err: errString(r.Leave(o.user, o.now))}
	case opRaise:
		return outcome{err: errString(r.Raise(o.user, o.now))}
	case opLower:
		return outcome{err: errString(r.Lower(o.user, o.now))}
	case opAppoint:
		return outcome{err: errString(r.Appoint(o.operator, o.target, o.now))}
	case opRevoke:
		return outcome{err: errString(r.Revoke(o.operator, o.target, o.now))}
	case opGrant:
		return outcome{err: errString(r.Grant(o.operator, o.now))}
	case opYield:
		return outcome{err: errString(r.Yield(o.user, o.now))}
	case opMute:
		return outcome{err: errString(r.Mute(o.operator, o.target, o.now))}
	case opUnmute:
		return outcome{err: errString(r.Unmute(o.operator, o.target, o.now))}
	case opSnapshot:
		snap, err := r.Snapshot(o.now)
		if err != nil {
			return outcome{err: errString(err)}
		}
		return outcome{err: "ok", snap: &snap}
	case opQueuePos:
		pos, err := r.QueuePos(o.user)
		return outcome{err: errString(err), pos: pos, hasPos: true}
	}
	panic("unreachable")
}

func applyNaive(r *naive.Room, o op) outcome {
	switch o.kind {
	case opJoin:
		return outcome{err: errString(r.Join(o.user, o.now))}
	case opLeave:
		return outcome{err: errString(r.Leave(o.user, o.now))}
	case opRaise:
		return outcome{err: errString(r.Raise(o.user, o.now))}
	case opLower:
		return outcome{err: errString(r.Lower(o.user, o.now))}
	case opAppoint:
		return outcome{err: errString(r.Appoint(o.operator, o.target, o.now))}
	case opRevoke:
		return outcome{err: errString(r.Revoke(o.operator, o.target, o.now))}
	case opGrant:
		return outcome{err: errString(r.Grant(o.operator, o.now))}
	case opYield:
		return outcome{err: errString(r.Yield(o.user, o.now))}
	case opMute:
		return outcome{err: errString(r.Mute(o.operator, o.target, o.now))}
	case opUnmute:
		return outcome{err: errString(r.Unmute(o.operator, o.target, o.now))}
	case opSnapshot:
		snap, err := r.Snapshot(o.now)
		if err != nil {
			return outcome{err: errString(err)}
		}
		return outcome{err: "ok", snap: &snap}
	case opQueuePos:
		pos, err := r.QueuePos(o.user)
		return outcome{err: errString(err), pos: pos, hasPos: true}
	}
	panic("unreachable")
}

// genOps 生成一条随机操作序列。now 大体单调不减，小概率回退；
// 小概率产生非法参数（空用户、now 越界）。
func genOps(rng *rand.Rand, n int, speakSecs int64) []op {
	users := []string{"u0", "u1", "u2", "u3", "u4", "u5", "u6", "u7"}
	pickUser := func() string {
		if rng.Intn(100) < 3 {
			return "" // 非法参数
		}
		if rng.Intn(100) < 4 {
			return "ghost" // 大概率不在室内
		}
		return users[rng.Intn(len(users))]
	}
	weights := []struct {
		kind   opKind
		weight int
	}{
		{opJoin, 14}, {opLeave, 7}, {opRaise, 16}, {opLower, 6},
		{opAppoint, 4}, {opRevoke, 3}, {opGrant, 8}, {opYield, 4},
		{opMute, 4}, {opUnmute, 3}, {opSnapshot, 12}, {opQueuePos, 8},
	}
	total := 0
	for _, w := range weights {
		total += w.weight
	}
	pickKind := func() opKind {
		x := rng.Intn(total)
		for _, w := range weights {
			if x < w.weight {
				return w.kind
			}
			x -= w.weight
		}
		return opJoin
	}
	ops := make([]op, 0, n)
	var now int64
	for i := 0; i < n; i++ {
		switch rng.Intn(100) {
		case 0, 1, 2: // 时钟回退
			now -= rng.Int63n(10)
		case 3: // now 越界
			now = meeting.MaxNow + 1
		default:
			if now > meeting.MaxNow {
				now = meeting.MaxNow
			}
			now += rng.Int63n(3*speakSecs + 1)
		}
		o := op{kind: pickKind(), now: now}
		switch o.kind {
		case opJoin, opLeave, opRaise, opLower, opYield, opQueuePos:
			o.user = pickUser()
		case opAppoint, opRevoke, opMute, opUnmute:
			o.operator = pickUser()
			o.target = pickUser()
		case opGrant:
			o.operator = pickUser()
		}
		ops = append(ops, o)
	}
	return ops
}

func outcomesEqual(a, b outcome) bool {
	if a.err != b.err || a.pos != b.pos || a.hasPos != b.hasPos {
		return false
	}
	if (a.snap == nil) != (b.snap == nil) {
		return false
	}
	if a.snap != nil && !reflect.DeepEqual(*a.snap, *b.snap) {
		return false
	}
	return true
}

// TestDifferentialAgainstNaive 至少 1500 组随机操作序列的差分对照。
// 每组打印输入（操作序列）、输出（每个操作的规范化结果）与判定依据
// （逐步结果一致性 + 快照深度相等）。
func TestDifferentialAgainstNaive(t *testing.T) {
	const sequences = 1500
	speakChoices := []int64{1, 2, 3, 5, 10, 60, 3600}
	capChoices := []int{1, 2, 3, 5, 8, 500}
	totalOps := 0
	for seq := 0; seq < sequences; seq++ {
		seed := int64(seq)*2654435761 + 12345
		rng := rand.New(rand.NewSource(seed))
		speakSecs := speakChoices[rng.Intn(len(speakChoices))]
		queueCap := capChoices[rng.Intn(len(capChoices))]
		nOps := 30 + rng.Intn(31) // 30..60 个操作
		ops := genOps(rng, nOps, speakSecs)

		opt, err := meeting.NewRoom(speakSecs, queueCap)
		if err != nil {
			t.Fatalf("seq %d: NewRoom: %v", seq, err)
		}
		nv, err := naive.NewRoom(speakSecs, queueCap)
		if err != nil {
			t.Fatalf("seq %d: naive.NewRoom: %v", seq, err)
		}

		t.Logf("seq=%d seed=%d S=%d Q=%d ops=%d", seq, seed, speakSecs, queueCap, nOps)
		for i, o := range ops {
			gotOpt := applyOpt(opt, o)
			gotNaive := applyNaive(nv, o)
			t.Logf("  op%03d %-28s opt=%s naive=%s", i, o, gotOpt.err, gotNaive.err)
			if !outcomesEqual(gotOpt, gotNaive) {
				t.Fatalf("seq %d op %d %s: opt=%+v naive=%+v", seq, i, o, gotOpt, gotNaive)
			}
		}
		totalOps += nOps
		t.Logf("  verdict: MATCH (%d ops, err/snapshot/pos all equal)", nOps)
	}
	t.Logf("differential test passed: %d sequences, %d ops total", sequences, totalOps)
}
