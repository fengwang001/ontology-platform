package capture

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// result 是 Manager 操作归一化后的结果，便于与朴素模型逐字段比对。
type result struct {
	val    int
	ok     bool
	reason Reason // ok == false 时有效
}

func errResult(op string, err error) result {
	var oe *OpError
	if errors.As(err, &oe) {
		return result{ok: false, reason: oe.Reason}
	}
	panic(fmt.Sprintf("%s returned non-OpError: %v", op, err))
}

// snapshot 完整刻画某一时刻的可观察状态，用于逐步全量比对。
type snapshot struct {
	top   int
	stack []int
	vars  map[int]varState
}

type varState struct {
	slot     int
	holders  int
	closed   bool
	storage  int
	shared   bool // 是否仍是所在槽的共享开放变量
	readable bool // 句柄读写是否仍有效
}

func snapshotManager(m *Manager) snapshot {
	s := snapshot{top: len(m.stack), vars: map[int]varState{}}
	s.stack = append(s.stack, m.stack...)
	for id, v := range m.vars {
		shared := m.open[v.slot] == v
		s.vars[id] = varState{
			slot:     v.slot,
			holders:  v.holders,
			closed:   v.closed,
			storage:  v.storage,
			shared:   shared,
			readable: v.holders > 0,
		}
	}
	return s
}

func snapshotNaive(n *naiveModel) snapshot {
	s := snapshot{top: len(n.stack), vars: map[int]varState{}}
	s.stack = append(s.stack, n.stack...)
	for id, v := range n.vars {
		shared := n.open[v.slot] == v
		s.vars[id] = varState{
			slot:     v.slot,
			holders:  v.holders,
			closed:   v.closed,
			storage:  v.storage,
			shared:   shared,
			readable: v.holders > 0,
		}
	}
	return s
}

func statesEqual(a, b snapshot) (bool, string) {
	if a.top != b.top {
		return false, fmt.Sprintf("top: %d != %d", a.top, b.top)
	}
	if fmt.Sprint(a.stack) != fmt.Sprint(b.stack) {
		return false, fmt.Sprintf("stack: %v != %v", a.stack, b.stack)
	}
	if len(a.vars) != len(b.vars) {
		return false, fmt.Sprintf("var count: %d != %d", len(a.vars), len(b.vars))
	}
	for id, av := range a.vars {
		bv, ok := b.vars[id]
		if !ok {
			return false, fmt.Sprintf("var %d missing in naive", id)
		}
		if av != bv {
			return false, fmt.Sprintf("var %d: %+v != %+v", id, av, bv)
		}
	}
	return true, ""
}

// op 是可在实现与朴素模型上重放的统一操作。
type op struct {
	kind string
	a, b int
}

func (o op) String() string {
	switch o.kind {
	case "push":
		return fmt.Sprintf("Push(%d)", o.a)
	case "capture":
		return fmt.Sprintf("Capture(slot=%d)", o.a)
	case "slot_get":
		return fmt.Sprintf("SlotGet(slot=%d)", o.a)
	case "slot_set":
		return fmt.Sprintf("SlotSet(slot=%d, val=%d)", o.a, o.b)
	case "handle_get":
		return fmt.Sprintf("HandleGet(h=%d)", o.a)
	case "handle_set":
		return fmt.Sprintf("HandleSet(h=%d, val=%d)", o.a, o.b)
	case "close":
		return fmt.Sprintf("CloseFrom(level=%d)", o.a)
	case "release":
		return fmt.Sprintf("Release(h=%d)", o.a)
	default:
		return o.kind
	}
}

func applyManager(m *Manager, o op) result {
	switch o.kind {
	case "push":
		return result{val: m.Push(o.a), ok: true}
	case "capture":
		v, err := m.Capture(o.a)
		if err != nil {
			return errResult("capture", err)
		}
		return result{val: v, ok: true}
	case "slot_get":
		v, err := m.SlotGet(o.a)
		if err != nil {
			return errResult("slot_get", err)
		}
		return result{val: v, ok: true}
	case "slot_set":
		if err := m.SlotSet(o.a, o.b); err != nil {
			return errResult("slot_set", err)
		}
		return result{ok: true}
	case "handle_get":
		v, err := m.HandleGet(o.a)
		if err != nil {
			return errResult("handle_get", err)
		}
		return result{val: v, ok: true}
	case "handle_set":
		if err := m.HandleSet(o.a, o.b); err != nil {
			return errResult("handle_set", err)
		}
		return result{ok: true}
	case "close":
		if err := m.CloseFrom(o.a); err != nil {
			return errResult("close_from", err)
		}
		return result{ok: true}
	case "release":
		if err := m.Release(o.a); err != nil {
			return errResult("release", err)
		}
		return result{ok: true}
	}
	panic("unknown op " + o.kind)
}

func applyNaive(n *naiveModel, o op) naiveResult {
	switch o.kind {
	case "push":
		return naiveResult{val: n.push(o.a), ok: true}
	case "capture":
		return n.capture(o.a)
	case "slot_get":
		return n.slotGet(o.a)
	case "slot_set":
		return n.slotSet(o.a, o.b)
	case "handle_get":
		return n.handleGet(o.a)
	case "handle_set":
		return n.handleSet(o.a, o.b)
	case "close":
		return n.closeFrom(o.a)
	case "release":
		return n.release(o.a)
	}
	panic("unknown op " + o.kind)
}

func sameResult(a result, b naiveResult) bool {
	if a.ok != b.ok {
		return false
	}
	if a.ok {
		return a.val == b.val
	}
	return a.reason == b.reason
}

// verdict 根据操作结果与规则说明给出“判定依据”，写入日志。
func verdict(o op, r result) string {
	switch {
	case r.ok && o.kind == "capture":
		return "同槽已有开放变量则复用句柄且持有数+1，否则分配新句柄"
	case r.ok && o.kind == "close":
		return "槽>=level 的开放变量复制槽值后转关闭并摘表，栈顶置为 level；level==栈顶为空操作"
	case r.ok && o.kind == "release":
		return "持有数-1；归零且仍开放则从共享表摘除"
	case r.ok:
		return "开放变量与栈槽双向可见，关闭变量只读写自身存储"
	case r.reason == ReasonCaptureSlotAboveTop:
		return "拒绝依据：捕获槽号不小于栈顶（空槽不能捕获），状态不变"
	case r.reason == ReasonCloseLevelOutOfRange:
		return "拒绝依据：level<0 或 level>栈顶，整体拒绝且状态不变"
	case r.reason == ReasonSlotOutOfRange:
		return "拒绝依据：栈槽越界（负槽号或槽号>=栈顶），状态不变"
	case r.reason == ReasonHandleNotFound:
		return "拒绝依据：句柄从未分配（与已释放完互斥）"
	case r.reason == ReasonHandleReleased:
		return "拒绝依据：句柄持有数已归零"
	default:
		return "未知"
	}
}

// runScenario 在实现与朴素模型上重放同一操作序列，逐步对照返回值、拒绝原因与完整状态，
// 日志打印每步输入、输出与判定依据。
func runScenario(t *testing.T, name string, ops []op) {
	t.Helper()
	m, n := New(), newNaiveModel()
	var log strings.Builder
	fmt.Fprintf(&log, "=== scenario %q ===\n", name)
	for i, o := range ops {
		rm := applyManager(m, o)
		rn := applyNaive(n, o)
		out := fmt.Sprintf("ok val=%d", rm.val)
		if !rm.ok {
			out = "REJECTED reason=" + rm.reason.String()
		}
		fmt.Fprintf(&log, "step %02d | 输入: %-28s | 输出: %-42s | 判定: %s\n",
			i+1, o.String(), out, verdict(o, rm))
		if !sameResult(rm, rn) {
			t.Fatalf("%s step %d op %s:\n%s\nmanager=%+v naive=%+v",
				name, i+1, o, log.String(), rm, rn)
		}
		sm, sn := snapshotManager(m), snapshotNaive(n)
		if eq, diff := statesEqual(sm, sn); !eq {
			t.Fatalf("%s step %d op %s state mismatch: %s\n%s\nimpl=%+v\nnaive=%+v",
				name, i+1, o, diff, log.String(), sm, sn)
		}
	}
	fmt.Fprintf(&log, "最终状态: top=%d stack=%v\n", m.Top(), m.stack)
	t.Logf("\n%s", log.String())
}
