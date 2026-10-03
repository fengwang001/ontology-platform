package replay

import (
	"ontology/code"
	"ontology/history"
)

// execResult 是一次纯本地模拟（不触碰日志）的结果。
type execResult struct {
	consumed  int             // 消费的历史条数（最终游标 c）
	newEvents []history.Event // 历史耗尽后续跑生成的事件
}

// simulator 持有单次 Run 的全部可变状态：
type simulator struct {
	hist []history.Event     // 开始时取得的快照（长度 L）
	c    int                 // 下一个待消费的历史下标
	p    map[string]struct{} // 本次运行已打补丁的 pid 集合 P
	cont []history.Event     // 续跑事件（待追加）
}

func newSimulator(snap []history.Event) *simulator {
	return &simulator{
		hist: snap,
		c:    0,
		p:    make(map[string]struct{}),
		cont: make([]history.Event, 0),
	}
}

// run 顺序执行顶层项；任何错误立即返回（调用方保证不再追加）。
func (s *simulator) run(items []code.Item) (execResult, error) {
	for _, it := range items {
		var err error
		if it.IsStep() {
			err = s.step(it.Name)
		} else {
			err = s.branch(it)
		}
		if err != nil {
			return execResult{}, err
		}
	}
	if s.c < len(s.hist) {
		return execResult{}, &IndexError{kind: ErrHistoryExtra, index: s.c}
	}
	return execResult{consumed: s.c, newEvents: s.cont}, nil
}

// step 执行一个普通 Step（包括 Branch 的 N/O 内部 Step）。
func (s *simulator) step(name []byte) error {
	if s.c < len(s.hist) {
		e := s.hist[s.c]
		if e.IsMarker() {
			return &IndexError{kind: ErrUnexpectedMarker, index: s.c}
		}
		if string(e.Name) != string(name) {
			return &IndexError{kind: ErrMismatch, index: s.c}
		}
		s.c++
		return nil
	}
	s.cont = append(s.cont, history.Step(name))
	return nil
}

// branch 执行 Branch(pid, N, O)，决策只依据“pid 是否在 P、历史是否耗尽、
// 下一事件是否标记”三个事实，顺序与 DESIGN 一致。
func (s *simulator) branch(it code.Item) error {
	pid := string(it.Pid)

	if _, patched := s.p[pid]; patched {
		return s.runSteps(it.New)
	}
	if s.c < len(s.hist) {
		switch e := s.hist[s.c]; {
		case e.IsMarker() && string(e.Pid) == pid:
			s.c++
			s.p[pid] = struct{}{}
			return s.runSteps(it.New)
		case e.IsMarker():
			return &IndexError{kind: ErrUnexpectedMarker, index: s.c}
		default: // 下一事件是 S：这是补丁前的旧历史，走 O，不加入 P
			return s.runSteps(it.Old)
		}
	}

	// 历史耗尽（续跑）：生成 M(pid)，加入 P，走 N。
	s.cont = append(s.cont, history.Marker(it.Pid))
	s.p[pid] = struct{}{}
	return s.runSteps(it.New)
}

// runSteps 顺序执行 N 或 O；code.Validate 已保证其中只含 Step。
func (s *simulator) runSteps(items []code.Item) error {
	for _, it := range items {
		if err := s.step(it.Name); err != nil {
			return err
		}
	}
	return nil
}
