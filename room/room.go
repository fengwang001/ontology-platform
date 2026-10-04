package room

import (
	"errors"
	"sort"
)

var (
	ErrInvalid   = errors.New("room: invalid argument")
	ErrDuplicate = errors.New("room: duplicate room id")
)

// 时间点与区间均为半开区间，单位分钟。

// Registry 保存已登记的手术间及其清洁间隔。
type Registry struct {
	turn map[string]int
}

func NewRegistry() *Registry {
	return &Registry{turn: map[string]int{}}
}

// Add 登记一间手术间。编号非空不重复，turn 取值 [0,240]。
func (r *Registry) Add(name string, turn int) error {
	if name == "" {
		return ErrInvalid
	}
	if turn < 0 || turn > 240 {
		return ErrInvalid
	}
	if _, ok := r.turn[name]; ok {
		return ErrDuplicate
	}
	r.turn[name] = turn
	return nil
}

// Has 判断手术间是否已登记。
func (r *Registry) Has(name string) bool {
	_, ok := r.turn[name]
	return ok
}

// Turn 返回手术间的清洁间隔。
func (r *Registry) Turn(name string) int {
	return r.turn[name]
}

// Names 返回所有已登记房间，编号字节序升序。
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.turn))
	for name := range r.turn {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type Interval struct {
	Start int64
	End   int64
}

// Conflict 按同间规则判定两台手术是否冲突：
// x.Start < y.End+turn 且 y.Start < x.End+turn。
// 前一台 end+turn 恰等于后一台 start 时相容。
func Conflict(x, y Interval, turn int) bool {
	t := int64(turn)
	return x.Start < y.End+t && y.Start < x.End+t
}
