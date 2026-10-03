package flexray

import "sync"

// ErrCode 标识操作被拒绝的原因。
type ErrCode int

const (
	// ErrInvalidParam 参数非法（构造或各参数越界、base 与 rep 不合法、长度越界）。
	ErrInvalidParam ErrCode = iota + 1
	// ErrInvalidID 编号超出 1 到 Ns+Nm。
	ErrInvalidID
	// ErrDuplicate Assign 的编号重复。
	ErrDuplicate
	// ErrNotAssigned Post 的编号未登记。
	ErrNotAssigned
	// ErrQueueFull 动态队列已满（每队列最多 8 条）。
	ErrQueueFull
)

// Error 包装一个可区分的拒绝原因。
type Error struct {
	Code ErrCode
	msg  string
}

func (e *Error) Error() string { return e.msg }

func errInvalidParam(msg string) *Error {
	return &Error{Code: ErrInvalidParam, msg: msg}
}

func errInvalidID() *Error   { return &Error{Code: ErrInvalidID, msg: "frame id out of range"} }
func errDuplicate() *Error   { return &Error{Code: ErrDuplicate, msg: "frame already assigned"} }
func errNotAssigned() *Error { return &Error{Code: ErrNotAssigned, msg: "frame not assigned"} }
func errQueueFull() *Error   { return &Error{Code: ErrQueueFull, msg: "dynamic queue full"} }

// SendItem 是 Cycle 发送清单中的一条：编号、标记与时隙位置
// （静态帧 Slot 为时隙号 1..Ns，动态帧 Start 为起始小时隙）。
type SendItem struct {
	ID    int
	Tag   string
	Slot  int
	Start int
}

// CycleResult 是一个周期运行的完整结果。
type CycleResult struct {
	C          int        // 本周期运行时的周期计数
	Sent       []SendItem // 按发送次序排列的清单
	Unused     int        // 本周期未用静态时隙数 u
	EmptyFrame int        // 本周期空帧数
}

// Stats 是累计统计。
type Stats struct {
	Sent       int // 累计发送（静态与动态合计）
	EmptyFrame int // 累计空帧
	Overwrite  int // 静态缓冲覆盖计数
}

type dynMsg struct {
	length int
	tag    string
}

// frame 保存单个编号的登记信息与缓冲/队列。
type frame struct {
	base      int
	rep       int
	assigned  bool
	staticBuf *string  // 静态帧唯一缓冲，nil 表示空
	queue     []dynMsg // 动态帧 FIFO
}

// Arbiter 是 FlexRay 式静态段/动态段仲裁器。
type Arbiter struct {
	mu sync.Mutex

	ns int
	nm int
	lt int
	c  int

	frames []*frame

	statSent      int
	statEmpty     int
	statOverwrite int
}

// New 构造仲裁器。Ns∈[1,64]，Nm∈[0,256]，Lt∈[0,Nm]。
func New(ns, nm, lt int) (*Arbiter, error) {
	if ns < 1 || ns > 64 {
		return nil, errInvalidParam("Ns must be in [1,64]")
	}
	if nm < 0 || nm > 256 {
		return nil, errInvalidParam("Nm must be in [0,256]")
	}
	if lt < 0 || lt > nm {
		return nil, errInvalidParam("Lt must be in [0,Nm]")
	}
	total := ns + nm
	a := &Arbiter{
		ns:     ns,
		nm:     nm,
		lt:     lt,
		frames: make([]*frame, total+1),
	}
	for idx := 1; idx <= total; idx++ {
		a.frames[idx] = &frame{}
	}
	return a, nil
}

// Assign 登记帧的激活周期：rep∈{1,2,4,8,16,32,64}，0≤base<rep。
func (a *Arbiter) Assign(id, base, rep int) error {
	validRep := false
	for _, r := range [...]int{1, 2, 4, 8, 16, 32, 64} {
		if rep == r {
			validRep = true
			break
		}
	}
	if !validRep || base < 0 || base >= rep {
		return errInvalidParam("rep must be one of 1,2,4,8,16,32,64 and 0<=base<rep")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if id < 1 || id > a.ns+a.nm {
		return errInvalidID()
	}
	f := a.frames[id]
	if f.assigned {
		return errDuplicate()
	}
	f.assigned = true
	f.base = base
	f.rep = rep
	return nil
}

// Post 投递消息。静态帧覆盖唯一缓冲，动态帧进入该编号的 FIFO。
func (a *Arbiter) Post(id, length int, tag string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id < 1 || id > a.ns+a.nm {
		return errInvalidID()
	}
	f := a.frames[id]
	if !f.assigned {
		return errNotAssigned()
	}
	if id > a.ns {
		if length < 1 || length > a.nm {
			return errInvalidParam("dynamic message length must be in [1,Nm]")
		}
		if len(f.queue) >= 8 {
			return errQueueFull()
		}
		f.queue = append(f.queue, dynMsg{length: length, tag: tag})
		return nil
	}
	if f.staticBuf != nil {
		a.statOverwrite++
	}
	buf := tag
	f.staticBuf = &buf
	return nil
}

// Cycle 运行当前周期 c 并推进 c。
func (a *Arbiter) Cycle() CycleResult {
	a.mu.Lock()
	defer a.mu.Unlock()

	c := a.c
	res := CycleResult{C: c, Sent: []SendItem{}}

	// 一、静态段：按编号 1..Ns 逐个处理。
	u := 0
	for id := 1; id <= a.ns; id++ {
		f := a.frames[id]
		if !f.assigned || c%f.rep != f.base {
			continue // 未登记或未激活：静默，消息保留
		}
		if f.staticBuf != nil {
			res.Sent = append(res.Sent, SendItem{
				ID:   id,
				Tag:  *f.staticBuf,
				Slot: id,
			})
			f.staticBuf = nil
			a.statSent++
		} else {
			u++
			res.EmptyFrame++
			a.statEmpty++
		}
	}
	res.Unused = u

	// 二、动态段：回收的静态时隙并入小时隙总数。
	n := a.nm + u
	k := a.ns + 1
	for i := 1; i <= n; i++ {
		if k <= a.ns+a.nm {
			f := a.frames[k]
			if f.assigned && c%f.rep == f.base && len(f.queue) > 0 &&
				i <= a.lt && i+f.queue[0].length-1 <= n {
				head := f.queue[0]
				res.Sent = append(res.Sent, SendItem{
					ID:    k,
					Tag:   head.tag,
					Start: i,
				})
				f.queue = f.queue[1:]
				a.statSent++
				i += head.length - 1 // 循环自身再 +1，合计前进 L
			}
		}
		k++ // 无论发送还是空过，k 每步只加 1
	}

	// 三、周期计数回绕。
	a.c = (c + 1) % 64
	return res
}

// Pending 返回某动态帧队列中消息的标记（队首在前）。
func (a *Arbiter) Pending(id int) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if id < 1 || id > a.ns+a.nm {
		return nil, errInvalidID()
	}
	f := a.frames[id]
	tags := make([]string, len(f.queue))
	for idx, m := range f.queue {
		tags[idx] = m.tag
	}
	return tags, nil
}

// Stats 返回累计统计快照。
func (a *Arbiter) Stats() Stats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Stats{
		Sent:       a.statSent,
		EmptyFrame: a.statEmpty,
		Overwrite:  a.statOverwrite,
	}
}

// C 返回当前周期计数。
func (a *Arbiter) C() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.c
}
