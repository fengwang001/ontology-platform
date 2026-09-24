// Package seqstate 维护单个 (pid, 分区) 在一个 epoch 下的序号状态：
// 最后接受的 lastSeq，以及最近 W 条被接受记录的 (seq, 位点) 保留窗口。
// 只负责规则第 4 步的判定，不依赖其他包。
package seqstate

import "errors"

// 可判定的哨兵错误。
var (
	ErrOutOfOrder       = errors.New("seqstate: out-of-order sequence")
	ErrDuplicateExpired = errors.New("seqstate: duplicate expired from window")
)

type entry struct{ seq, offset int }

// State 是一个 (pid, 分区) 的序号状态；尚未接受任何记录时 has 为假。
type State struct {
	has    bool
	last   int
	window []entry // 最旧在前
	max    int     // 窗口容量 W
}

// New 创建容量为 windowCap 的空状态。
func New(windowCap int) *State { return &State{max: windowCap} }

// Judge 执行规则第 4 步判定，不修改任何状态。
// 返回 (位点, 是否重复, 错误)；err==nil 且 dup==false 表示应当追加。
func (s *State) Judge(seq int) (offset int, dup bool, err error) {
	if !s.has {
		if seq != 0 {
			return 0, false, ErrOutOfOrder
		}
		return 0, false, nil
	}
	if seq == s.last+1 {
		return 0, false, nil
	}
	if seq <= s.last {
		for _, e := range s.window {
			if e.seq == seq {
				return e.offset, true, nil
			}
		}
		return 0, false, ErrDuplicateExpired
	}
	return 0, false, ErrOutOfOrder
}

// Accept 在追加成功后记账：lastSeq=seq，(seq, offset) 进窗口，超容量丢最旧一条。
func (s *State) Accept(seq, offset int) {
	s.has = true
	s.last = seq
	s.window = append(s.window, entry{seq, offset})
	if len(s.window) > s.max {
		s.window = s.window[1:]
	}
}
