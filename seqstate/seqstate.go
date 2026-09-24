// Package seqstate 维护单个 (producer, 分区) 在“当前 epoch 下”的序号状态：
// lastSeq 与最近 W 条被接受记录的 (seq, 位点) 保留窗口，以及规则第 4 步判定。
// 本包不依赖工程内任何其他包。
package seqstate

// Decision 是规则第 4 步的判定结果。
type Decision int

const (
	Append           Decision = iota // seq 符合预期，调用方应当追加并随后 Commit
	Duplicate                        // seq 在保留窗口内：返回旧位点，不追加
	ExpiredDuplicate                 // seq <= lastSeq 但已滑出窗口
	OutOfOrder                       // 跳号，或无状态时首条 seq != 0
)

// State 是一个 (pid, 分区) 的序号状态；零值不可用，须经 New 构造。
type State struct {
	window int
	active bool
	last   int64
	win    []slot
}

type slot struct {
	seq int64
	off int64
}

// New 创建保留窗口为 W 的序号状态。
func New(window int) *State {
	return &State{window: window, win: make([]slot, 0, window+1)}
}

// Reset 清空全部序号状态，等价于回到“该 (pid, 分区) 从未接受过记录”。
func (s *State) Reset() {
	s.active = false
	s.last = 0
	s.win = s.win[:0]
}

// Check 执行规则第 4 步判定；结果为 Duplicate 时第二个返回值是当初分配的位点。
// Check 是只读的：只有调用方真正追加成功后调用 Commit，状态才会改变。
func (s *State) Check(seq int64) (Decision, int64) {
	if !s.active {
		if seq == 0 {
			return Append, 0
		}
		return OutOfOrder, 0
	}
	switch {
	case seq == s.last+1:
		return Append, 0
	case seq > s.last+1:
		return OutOfOrder, 0
	default: // seq <= last：在保留窗口内线性查找
		for _, e := range s.win {
			if e.seq == seq {
				return Duplicate, e.off
			}
		}
		return ExpiredDuplicate, 0
	}
}

// Commit 在记录实际追加成功后登记 (seq, 位点)，推进 lastSeq 并滚动保留窗口。
func (s *State) Commit(seq, off int64) {
	s.active = true
	s.last = seq
	s.win = append(s.win, slot{seq: seq, off: off})
	if len(s.win) > s.window {
		copy(s.win, s.win[1:]) // 丢弃最旧一条，保持底层数组不扩容
		s.win = s.win[:s.window]
	}
}
