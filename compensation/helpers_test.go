package compensation

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// addOp 是测试用的交换律子操作：对单个键 Add(delta)。
type addOp struct {
	name  string
	key   string
	delta int64
	fail  bool
}

func (o *addOp) Name() string   { return o.name }
func (o *addOp) Keys() []string { return []string{o.key} }
func (o *addOp) Apply(g *Graph) error {
	if o.fail {
		return errors.New("apply injected failure: " + o.name)
	}
	g.Add(o.key, o.delta)
	return nil
}

type addInverse struct {
	name  string
	key   string
	delta int64
	fail  bool
}

func (o *addInverse) Name() string   { return o.name }
func (o *addInverse) Keys() []string { return []string{o.key} }
func (o *addInverse) Undo(g *Graph) error {
	if o.fail {
		return errors.New("undo injected failure: " + o.name)
	}
	g.Add(o.key, -o.delta)
	return nil
}

// step 构造一个可补偿的 Add 子操作声明。
func step(name, key string, delta int64, applyFail, undoFail bool) StepSpec {
	op := &addOp{name: name, key: key, delta: delta, fail: applyFail}
	return StepSpec{
		Op: op,
		MakeInverse: func() Inverse {
			return &addInverse{name: "inv-" + name, key: key, delta: delta, fail: undoFail}
		},
	}
}

// memLogger 收集补偿尝试日志，供测试断言与打印。
type memLogger struct {
	mu      sync.Mutex
	entries []LogEntry
}

func (l *memLogger) LogAttempt(e LogEntry) {
	l.mu.Lock()
	l.entries = append(l.entries, e)
	l.mu.Unlock()
}

func (l *memLogger) dump() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var b strings.Builder
	for _, e := range l.entries {
		fmt.Fprintf(&b, "[comp-attempt] action=%s branch=%s step=%d inverse=%s input=%q allowed=%t result=%s reason=%s\n",
			e.ActionName, e.BranchName, e.StepIndex, e.InverseName, e.Input,
			e.Allowed, e.Result, e.Reason)
	}
	return b.String()
}

func (l *memLogger) count(result string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.entries {
		if e.Result == result {
			n++
		}
	}
	return n
}
