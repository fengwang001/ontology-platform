package mirror_test

import (
	"errors"

	"ontology/diff"
	"ontology/mirror"
)

// 朴素模拟的操作类型。
type opKind int

const (
	opMirror opKind = iota
	opPrimary
	opDoneOK
	opDoneErr
)

type op struct {
	kind     opKind
	reqID    string
	method   string
	bodyLen  int64
	headers  map[string]string
	now      int64
	status   int
	fields   map[string]string
	mirrorID int64
}

type naiveSlot struct {
	id              int64
	primaryOK       bool
	doneOK, doneErr bool
	pr, sr          diff.Response
}

// naive 是严格按题目规则逐步写成的独立参考实现。
type naive struct {
	cfg         mirror.Config
	c, s        int64
	pausedUntil int64
	maxNow      int64
	nextID      int64
	inFlight    int
	reqs        map[string]*naiveSlot
	ids         map[int64]*naiveSlot

	accepted, skUnsafe, skBody, skPause, skSample, skBusy int64
	dispatched, completed, shadowErrors                   int64
	identical, compatible, breaking                       int64
	pendingPrimary, pendingDone                           int64
}

func newNaive(cfg mirror.Config) *naive {
	return &naive{cfg: cfg, reqs: map[string]*naiveSlot{}, ids: map[int64]*naiveSlot{}}
}

func validMethod(m string) bool {
	switch m {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

// mirrorOp 返回 (结果码, mirrorID)。结果码：dispatch/skip-1..5/errInvalid/errTime/errClock/errDup。
func (n *naive) mirrorOp(o op) (string, int64) {
	// 参数非法
	if o.reqID == "" || !validMethod(o.method) || o.bodyLen < 0 || o.bodyLen > 1e12 {
		return "invalid", 0
	}
	seen := map[string]bool{}
	for h := range o.headers {
		if h == "" {
			return "invalid", 0
		}
		l := lower(h)
		if seen[l] {
			return "invalid", 0
		}
		seen[l] = true
	}
	// 时间非法
	if o.now < 0 || o.now > 1e15 {
		return "time", 0
	}
	// 时钟回退
	if o.now < n.maxNow {
		return "clock", 0
	}
	n.maxNow = o.now
	// 重复
	if _, ok := n.reqs[o.reqID]; ok {
		return "dup", 0
	}
	n.accepted++
	safe := o.method == "GET" || o.method == "HEAD"
	if !safe && !n.cfg.AllowUnsafe {
		n.skUnsafe++
		return "skip1", 0
	}
	if o.bodyLen > n.cfg.Bm {
		n.skBody++
		return "skip2", 0
	}
	if o.now < n.pausedUntil {
		n.skPause++
		return "skip3", 0
	}
	n.c++
	if n.c%n.cfg.K != 0 {
		n.skSample++
		return "skip4", 0
	}
	if int64(n.inFlight) >= int64(n.cfg.Cm) {
		n.skBusy++
		return "skip5", 0
	}
	n.nextID++
	id := n.nextID
	n.inFlight++
	n.dispatched++
	sl := &naiveSlot{id: id}
	n.reqs[o.reqID] = sl
	n.ids[id] = sl
	return "dispatch", id
}

func (n *naive) primaryOp(o op) string {
	if o.reqID == "" || o.status < 0 || o.status > 1e9 {
		return "invalid"
	}
	for f := range o.fields {
		if f == "" {
			return "invalid"
		}
	}
	sl, ok := n.reqs[o.reqID]
	if !ok {
		return "unknown"
	}
	if sl.primaryOK || sl.doneErr {
		return "dup"
	}
	sl.primaryOK = true
	sl.pr = diff.Response{Status: o.status, Fields: o.fields}
	if sl.doneOK {
		n.pendingPrimary--
		n.countClass(sl)
	} else {
		n.pendingDone++
	}
	return "ok"
}

func (n *naive) doneOp(o op, isErr bool) string {
	if o.mirrorID < 1 {
		return "invalid"
	}
	if !isErr && (o.status < 0 || o.status > 1e9) {
		return "invalid"
	}
	if o.now < 0 || o.now > 1e15 {
		return "time"
	}
	if o.now < n.maxNow {
		return "clock"
	}
	n.maxNow = o.now
	sl, ok := n.ids[o.mirrorID]
	if !ok {
		return "unknown"
	}
	if sl.doneOK || sl.doneErr {
		return "dup"
	}
	n.inFlight--
	n.completed++
	if isErr {
		sl.doneErr = true
		n.shadowErrors++
		if o.now >= n.pausedUntil {
			n.s++
			if int64(n.s) >= int64(n.cfg.E) {
				n.pausedUntil = o.now + n.cfg.P
				n.s = 0
			}
		}
		if sl.primaryOK {
			n.pendingDone--
		}
		return "ok"
	}
	sl.doneOK = true
	sl.sr = diff.Response{Status: o.status, Fields: o.fields}
	if o.now >= n.pausedUntil {
		n.s = 0
	}
	if sl.primaryOK {
		n.pendingDone--
		n.countClass(sl)
	} else {
		n.pendingPrimary++
	}
	return "ok"
}

func (n *naive) countClass(sl *naiveSlot) {
	c := diff.Classify(sl.pr, sl.sr, n.cfg.Ignore)
	switch c {
	case diff.Identical:
		n.identical++
	case diff.Compatible:
		n.compatible++
	case diff.Breaking:
		n.breaking++
	}
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func realErrName(e error) string {
	switch {
	case e == nil:
		return "ok"
	case errors.Is(e, mirror.ErrInvalid):
		return "invalid"
	case errors.Is(e, mirror.ErrTime):
		return "time"
	case errors.Is(e, mirror.ErrClock):
		return "clock"
	case errors.Is(e, mirror.ErrDuplicate):
		return "dup"
	case errors.Is(e, mirror.ErrUnknown):
		return "unknown"
	default:
		return "other"
	}
}
