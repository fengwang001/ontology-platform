// Package phase 是 Phaser 的相位状态机：未到达计数、party 集合、
// 推进判定（未到达降 0 且 party>0）与终止态。不依赖其他包。
package phase

import (
	"errors"
	"sort"
)

// 可判定的哨兵错误，四者互不相同。
var (
	ErrNoParties  = errors.New("phase: party count must be positive")
	ErrUnknownID  = errors.New("phase: unknown party id")
	ErrDuplicate  = errors.New("phase: party already arrived in this phase")
	ErrTerminated = errors.New("phase: phaser is terminated")
)

// State 是相位状态机，非并发安全（并发控制由上层 bar 负责）。
type State struct {
	phase     int
	nextID    int
	parties   map[int]struct{} // 已登记 party
	unarrived map[int]struct{} // 当前相位未到达 party（parties 的子集）
	term      bool
	checked   int // 最近一次 Arrive 为推进判定检查过的 party 数（非导出，仅供包内测试）
}

// New 创建初始相位 0、含 n 个 party（编号 0..n-1）的状态机。
func New(n int) (*State, error) {
	if n <= 0 {
		return nil, ErrNoParties
	}
	s := &State{
		parties:   make(map[int]struct{}, n),
		unarrived: make(map[int]struct{}, n),
		nextID:    n,
	}
	for i := 0; i < n; i++ {
		s.parties[i] = struct{}{}
		s.unarrived[i] = struct{}{}
	}
	return s, nil
}

// Phase 返回当前相位；终止态返回 -1。
func (s *State) Phase() int {
	if s.term {
		return -1
	}
	return s.phase
}

// Terminated 报告是否已进入终止态。
func (s *State) Terminated() bool { return s.term }

// Register 新增一个 party，加入当前相位（对当前相位未到达）。
func (s *State) Register() (id, phase int, err error) {
	if s.term {
		return 0, -1, ErrTerminated
	}
	id = s.nextID
	s.nextID++
	s.parties[id] = struct{}{}
	s.unarrived[id] = struct{}{}
	return id, s.phase, nil
}

// Arrive 把 id 标记为在当前相位已到达，返回到达时的相位（推进前）。
func (s *State) Arrive(id int) (int, error) {
	if s.term {
		return -1, ErrTerminated
	}
	if _, ok := s.parties[id]; !ok {
		return -1, ErrUnknownID
	}
	if _, ok := s.unarrived[id]; !ok {
		return -1, ErrDuplicate
	}
	delete(s.unarrived, id)
	s.checked = 1 // 推进判定只递减未到达计数并判 0：恰好检查 1 个 party
	p := s.phase
	s.maybeAdvance()
	return p, nil
}

// Deregister 把 id 从 party 集合移除；party 数降 0 时进入终止态。
func (s *State) Deregister(id int) error {
	if s.term {
		return ErrTerminated
	}
	if _, ok := s.parties[id]; !ok {
		return ErrUnknownID
	}
	delete(s.parties, id)
	delete(s.unarrived, id)
	if len(s.parties) == 0 {
		s.term = true
	}
	return nil
}

// maybeAdvance 是唯一推进点：未到达降 0 且 party>0 时相位 +1、全体重置。
func (s *State) maybeAdvance() {
	if len(s.unarrived) == 0 && len(s.parties) > 0 {
		s.phase++
		for id := range s.parties {
			s.unarrived[id] = struct{}{}
		}
	}
}

// Snapshot 返回（相位，party 集合，未到达集合），两个集合均升序。
func (s *State) Snapshot() (int, []int, []int) {
	return s.Phase(), sorted(s.parties), sorted(s.unarrived)
}

func sorted(m map[int]struct{}) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
