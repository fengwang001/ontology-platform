package replay

import (
	"bytes"
	"fmt"

	"ontology/code"
	"ontology/history"
)

// Result 是一次成功重放的结果。
type Result struct {
	Consumed  int // 消费的历史事件条数（等于快照长度 L）
	Continued int // 续跑产生并追加的事件条数
}

// Run 先取 wf 的一次快照（长度 L），再执行 code 并逐条比对；
// 成功时把续跑事件以 Append(wf, L, pending) 一次原子写入。
//
// 拒绝优先级：参数非法（空 wf）与 code.ErrCode > 执行期非确定性
// 错误（ErrMismatch / ErrUnexpectedMarker / ErrHistoryExtra）>
// 追加期 history.ErrConflict / history.ErrCapacity。任何错误都
// 不改历史；ErrConflict/ErrCapacity 时调用方可重新 Run。
func Run(store *history.Store, wf []byte, c code.Code) (Result, error) {
	if len(wf) == 0 {
		return Result{}, fmt.Errorf("%w: empty workflow id", history.ErrInvalid)
	}
	if err := code.Validate(c); err != nil {
		return Result{}, err
	}
	snap := store.Snapshot(wf)
	res, pending, _, err := execute(snap, c)
	if err != nil {
		return Result{}, err
	}
	if len(pending) > 0 {
		if err := store.Append(wf, len(snap), pending); err != nil {
			return Result{}, err
		}
	}
	return res, nil
}

// execute 在快照上执行代码，返回结果、待追加事件与比较次数。
// 比较次数（非导出计数器）恒等于消费条数加续跑条数。
func execute(snap []history.Event, c code.Code) (Result, []history.Event, int, error) {
	r := &run{hist: snap, patched: make(map[string]struct{})}
	if err := r.execItems([]code.Item(c)); err != nil {
		return Result{}, nil, r.comparisons, err
	}
	if r.cursor < len(snap) {
		return Result{}, nil, r.comparisons, &HistoryExtraError{Index: r.cursor}
	}
	res := Result{Consumed: r.cursor, Continued: len(r.pending)}
	return res, r.pending, r.comparisons, nil
}

// run 是一次重放的执行状态。
type run struct {
	hist        []history.Event     // 快照，长度 L
	cursor      int                 // 下一个待消费的历史下标 c
	patched     map[string]struct{} // 本次运行已打补丁的 pid 集合 P
	pending     []history.Event     // 待追加的续跑事件
	comparisons int                 // 非导出比较计数器：等于消费条数+续跑条数
}

func (r *run) execItems(items []code.Item) error {
	for _, it := range items {
		var err error
		switch v := it.(type) {
		case code.Step:
			err = r.execStep(v.Name)
		case code.Branch:
			err = r.execBranch(v)
		default:
			err = fmt.Errorf("%w: unknown item type %T", code.ErrCode, it)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// execStep 执行 Step(name)：历史未耗尽则必须匹配 S(name) 并消费，
// 否则生成 S(name) 记入待追加。
func (r *run) execStep(name []byte) error {
	if r.cursor < len(r.hist) {
		ev := r.hist[r.cursor]
		if ev.Kind == history.Marker {
			return &UnexpectedMarkerError{Index: r.cursor, Pid: ev.Data}
		}
		if !bytes.Equal(ev.Data, name) {
			return &MismatchError{Index: r.cursor, Have: ev.Data, Want: name}
		}
		r.cursor++
		r.comparisons++
		return nil
	}
	r.pending = append(r.pending, history.S(name))
	r.comparisons++
	return nil
}

// execBranch 执行 Branch(pid, N, O)：见包注释与 DESIGN.md 的取舍。
func (r *run) execBranch(b code.Branch) error {
	pid := string(b.Pid)
	if _, ok := r.patched[pid]; ok {
		return r.execItems(b.New) // 已打补丁：走 N，不写标记
	}
	if r.cursor < len(r.hist) {
		ev := r.hist[r.cursor]
		if ev.Kind == history.Marker {
			if !bytes.Equal(ev.Data, b.Pid) {
				return &UnexpectedMarkerError{Index: r.cursor, Pid: ev.Data}
			}
			r.cursor++ // 消费 M(pid)，记入 P，走 N
			r.comparisons++
			r.patched[pid] = struct{}{}
			return r.execItems(b.New)
		}
		return r.execItems(b.Old) // 下一事件是 S：走 O，不加入 P
	}
	// 历史耗尽：生成 M(pid)，加入 P，走 N
	r.pending = append(r.pending, history.M(b.Pid))
	r.comparisons++
	r.patched[pid] = struct{}{}
	return r.execItems(b.New)
}
