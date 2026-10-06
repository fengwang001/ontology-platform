package room

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// opKind 标识随机/场景序列中的一种操作。
type opKind int

const (
	opJoin opKind = iota
	opReady
	opUnready
	opLeave
	opEnd
	opReport
	opSnapshot
)

type op struct {
	kind   opKind
	user   string
	winner string
	now    int64
}

func (o op) String() string {
	switch o.kind {
	case opJoin:
		return fmt.Sprintf("Join(%q,%d)", o.user, o.now)
	case opReady:
		return fmt.Sprintf("SetReady(%q,true,%d)", o.user, o.now)
	case opUnready:
		return fmt.Sprintf("SetReady(%q,false,%d)", o.user, o.now)
	case opLeave:
		return fmt.Sprintf("Leave(%q,%d)", o.user, o.now)
	case opEnd:
		return fmt.Sprintf("End(%q,%d)", o.user, o.now)
	case opReport:
		return fmt.Sprintf("Report(%q,%q,%d)", o.user, o.winner, o.now)
	default:
		return fmt.Sprintf("Snapshot(%d)", o.now)
	}
}

// dual 同时驱动高效实现与朴素模型，逐条比对结果。
// trace 累积输入、输出与判定依据，可完整复现任意分歧。
type dual struct {
	t     *testing.T
	cfg   Config
	fast  *Room
	slow  *naiveRoom
	trace strings.Builder
	step  int
}

func newDual(t *testing.T, cfg Config) *dual {
	t.Helper()
	f, err := New(cfg)
	if err != nil {
		t.Fatalf("New 不应失败: %v", err)
	}
	d := &dual{t: t, cfg: cfg, fast: f, slow: newNaive(cfg)}
	fmt.Fprintf(&d.trace, "config L=%d U=%d C=%d R=%d\n", cfg.L, cfg.U, cfg.C, cfg.R)
	return d
}

func reason(err error) string {
	if err == nil {
		return "ok"
	}
	if oe, ok := err.(OpError); ok {
		return string(oe.Reason)
	}
	return err.Error()
}

func normSnap(s Snapshot) Snapshot {
	if len(s.Present) == 0 {
		s.Present = nil
	}
	if len(s.Roster) == 0 {
		s.Roster = nil
	}
	return s
}

// run 执行一条操作并比对。basis 为人工判定依据说明（写进日志）。
func (d *dual) run(o op, basis string) {
	d.t.Helper()
	d.step++
	var ef, es error
	switch o.kind {
	case opJoin:
		ef = d.fast.Join(o.user, o.now)
		es = d.slow.join(o.user, o.now)
	case opReady:
		ef = d.fast.SetReady(o.user, true, o.now)
		es = d.slow.setReady(o.user, true, o.now)
	case opUnready:
		ef = d.fast.SetReady(o.user, false, o.now)
		es = d.slow.setReady(o.user, false, o.now)
	case opLeave:
		ef = d.fast.Leave(o.user, o.now)
		es = d.slow.leave(o.user, o.now)
	case opEnd:
		ef = d.fast.End(o.user, o.now)
		es = d.slow.end(o.user, o.now)
	case opReport:
		ef = d.fast.Report(o.user, o.winner, o.now)
		es = d.slow.report(o.user, o.winner, o.now)
	case opSnapshot:
		_, ef = d.fast.Snapshot(o.now)
		_, es = d.slow.snapshot(o.now)
	}
	fmt.Fprintf(&d.trace, "#%03d %-34s => fast=%s slow=%s | %s\n",
		d.step, o.String(), reason(ef), reason(es), basis)
	if reason(ef) != reason(es) {
		d.failf("拒绝原因不一致: %s vs %s", reason(ef), reason(es))
	}
	sf2, _ := d.fast.Snapshot(o.now)
	ss2, _ := d.slow.snapshot(o.now)
	if !reflect.DeepEqual(normSnap(sf2), normSnap(ss2)) {
		d.failf("快照不一致\n fast=%#v\n slow=%#v", sf2, ss2)
	}
}

func (d *dual) failf(format string, args ...any) {
	d.t.Helper()
	d.t.Fatalf("%s\n--- trace ---\n%s", fmt.Sprintf(format, args...), d.trace.String())
}

// log 打印当前完整 trace（供 -v 下复现判定依据）。
func (d *dual) log() {
	d.t.Helper()
	d.t.Log("\n" + d.trace.String())
}
