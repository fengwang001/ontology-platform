// Package chanbuf 维护单个输入通道的阻塞状态、已到达屏障数与 FIFO 缓冲。
// 本包不依赖项目中的其他包。
package chanbuf

// Kind 区分输入元素种类。
type Kind uint8

const (
	KindRecord Kind = iota + 1
	KindBarrier
)

// Item 是一个输入元素：记录用 Key/Val，屏障用 ID（检查点编号）。
type Item struct {
	Ch   int
	Kind Kind
	Key  string
	Val  int64
	ID   int64
}

// Out 是输出流元素：记录原样转发，屏障 Ch=-1。
type Out = Item

// entry 在缓冲内给元素贴上全局到达序号，供两通道缓冲按到达顺序归并重放。
type entry struct {
	it  Item
	seq int64
}

// State 是单个通道的状态：是否阻塞、已到达屏障个数、FIFO 缓冲。
// 阻塞判定只看 blocked 标志（O(1)），从不扫描缓冲。
type State struct {
	blocked  bool
	barriers int64
	buf      []entry
}

// New 创建一个空通道状态。
func New() *State { return &State{} }

// Blocked 报告通道当前是否阻塞。
func (s *State) Blocked() bool { return s.blocked }

// Barriers 返回该通道已到达的屏障个数（按到达计，含进入缓冲的屏障）。
func (s *State) Barriers() int64 { return s.barriers }

// Len 返回缓冲元素个数。
func (s *State) Len() int { return len(s.buf) }

// MarkBarrier 记录一个屏障到达：计数加一且通道进入阻塞（调用前必未阻塞）。
func (s *State) MarkBarrier() {
	s.barriers++
	s.blocked = true
}

// RestoreMark 撤销一次 MarkBarrier：屏障计数减一，并把阻塞态恢复为 prevBlocked。
// 仅供批处理失败回滚使用。
func (s *State) RestoreMark(prevBlocked bool) {
	s.barriers--
	s.blocked = prevBlocked
}

// Unblock 解除阻塞。
func (s *State) Unblock() { s.blocked = false }

// Block 仅置阻塞、不增加屏障计数：重放缓冲中早已到达的屏障时使用。
func (s *State) Block() { s.blocked = true }

// Push 把元素按到达顺序追加到缓冲尾部，seq 为全局到达序号。
func (s *State) Push(it Item, seq int64) {
	s.buf = append(s.buf, entry{it: it, seq: seq})
}

// PushFront 把元素连同其到达序号插回队首，仅供回滚使用。
func (s *State) PushFront(it Item, seq int64) {
	s.buf = append(s.buf, entry{})
	copy(s.buf[1:], s.buf)
	s.buf[0] = entry{it: it, seq: seq}
}

// Trim 截断缓冲到前 n 个元素，仅供回滚使用。
func (s *State) Trim(n int) { s.buf = s.buf[:n] }

// HeadSeq 返回队首元素的全局到达序号；缓冲为空时 ok=false。
// 对齐时用它 O(1) 比较两通道队首先后，不扫描缓冲。
func (s *State) HeadSeq() (seq int64, ok bool) {
	if len(s.buf) == 0 {
		return 0, false
	}
	return s.buf[0].seq, true
}

// Pop 取出并移除队首元素（FIFO），同时返回其全局到达序号。
func (s *State) Pop() (it Item, seq int64, ok bool) {
	if len(s.buf) == 0 {
		return Item{}, 0, false
	}
	e := s.buf[0]
	s.buf = s.buf[1:]
	return e.it, e.seq, true
}
