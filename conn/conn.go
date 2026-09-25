// Package conn 单连接拆除状态机：当前状态、enterTime、动作计数、转移表。不依赖其他包。
package conn

import (
	"errors"
	"sync"
)

// State 是连接拆除状态机的状态。
type State int

const Established, FinWait1, FinWait2, CloseWait, LastAck, Closing, TimeWait, Closed State = 0, 1, 2, 3, 4, 5, 6, 7

var names = []string{"ESTABLISHED", "FIN_WAIT_1", "FIN_WAIT_2", "CLOSE_WAIT", "LAST_ACK", "CLOSING", "TIME_WAIT", "CLOSED"}

func (s State) String() string { return names[s] }

// Event 是驱动状态机的事件。
type Event int

const EvClose, EvACK, EvFIN, EvData, EvRST, EvTick Event = 0, 1, 2, 3, 4, 5

// Action 是转移时产生的可观察动作。
type Action int

const ActNone, ActFIN, ActACK Action = 0, 1, 2

// 可判定哨兵错误，互不相同。
var (
	ErrIllegalACK  = errors.New("conn: illegal ACK for current state")
	ErrIllegalFIN  = errors.New("conn: illegal FIN for current state")
	ErrIllegalData = errors.New("conn: illegal DATA for current state")
	ErrClockBack   = errors.New("conn: clock moved backwards")
	ErrIllegal     = errors.New("conn: illegal event for current state")
)

// illegal 按事件（索引即 Event 值）给出非法时的哨兵错误。
var illegal = []error{ErrIllegal, ErrIllegalACK, ErrIllegalFIN, ErrIllegalData, ErrIllegal, ErrIllegal}

type rule struct {
	next   State
	act    Action
	tw     bool // 进入/重启 TIME_WAIT：enterTime = now
	expire bool // Tick@TIME_WAIT：仅当 now-enter >= 2MSL 才迁到 next
}

type key struct {
	s State
	e Event
}

// table 是转移表的唯一事实源；RST（7 条）与 Tick（8 条）在 init 补齐。
var table = map[key]rule{
	{Established, EvClose}: {FinWait1, ActFIN, false, false},
	{CloseWait, EvClose}:   {LastAck, ActFIN, false, false},
	{FinWait1, EvACK}:      {FinWait2, ActNone, false, false},
	{Closing, EvACK}:       {TimeWait, ActNone, true, false},
	{LastAck, EvACK}:       {Closed, ActNone, false, false},
	{Established, EvFIN}:   {CloseWait, ActACK, false, false},
	{FinWait1, EvFIN}:      {Closing, ActACK, false, false},
	{FinWait2, EvFIN}:      {TimeWait, ActACK, true, false},
	{TimeWait, EvFIN}:      {TimeWait, ActACK, true, false},
	{Established, EvData}:  {Established, ActNone, false, false},
	{FinWait1, EvData}:     {FinWait1, ActNone, false, false},
	{FinWait2, EvData}:     {FinWait2, ActNone, false, false},
	{CloseWait, EvData}:    {CloseWait, ActNone, false, false},
}

func init() {
	for s := Established; s < Closed; s++ { // RST：除 CLOSED 外一律立即 CLOSED
		table[key{s, EvRST}] = rule{Closed, ActNone, false, false}
	}
	for s := Established; s <= Closed; s++ { // Tick：非 TIME_WAIT 为合法 no-op
		table[key{s, EvTick}] = rule{s, ActNone, false, false}
	}
	table[key{TimeWait, EvTick}] = rule{Closed, ActNone, false, true}
}

// Conn 是单连接状态机，用 New 构造。
type Conn struct {
	mu      sync.RWMutex
	state   State
	enter   int64 // TIME_WAIT 基准时间
	last    int64 // 上一次携带 now 的事件的 now
	hasNow  bool
	twoMSL  int64
	sentFIN int
	sentACK int
	checks  int // 非导出：每次分派检查过的转移规则条数，仅供包内测试读取
}

// New 创建状态机，twoMSL 为 TIME_WAIT 计时常量（2MSL，>0），初始 ESTABLISHED。
func New(twoMSL int64) *Conn { return &Conn{state: Established, twoMSL: twoMSL} }

// carriesNow 报告事件是否携带逻辑时钟。
func carriesNow(e Event) bool { return e == EvACK || e == EvFIN || e == EvTick }

// Apply 分派一个事件：查转移表（计 1 次检查）并原子执行。
// 时钟回退与非法事件整体失败、零副作用。
func (c *Conn) Apply(e Event, now int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if carriesNow(e) && c.hasNow && now < c.last {
		return ErrClockBack
	}
	r, ok := table[key{c.state, e}]
	c.checks++
	if !ok {
		return illegal[e]
	}
	if r.expire && now-c.enter < c.twoMSL { // 未到 2MSL，保持 TIME_WAIT
		r.next = c.state
	}
	c.state = r.next
	if r.tw {
		c.enter = now
	}
	switch r.act {
	case ActFIN:
		c.sentFIN++
	case ActACK:
		c.sentACK++
	}
	if carriesNow(e) {
		c.last, c.hasNow = now, true
	}
	return nil
}

// State 返回当前状态。EnterTime 返回 TIME_WAIT 基准。SentFIN/SentACK 返回动作计数。
func (c *Conn) State() State     { c.mu.RLock(); defer c.mu.RUnlock(); return c.state }
func (c *Conn) EnterTime() int64 { c.mu.RLock(); defer c.mu.RUnlock(); return c.enter }
func (c *Conn) SentFIN() int     { c.mu.RLock(); defer c.mu.RUnlock(); return c.sentFIN }
func (c *Conn) SentACK() int     { c.mu.RLock(); defer c.mu.RUnlock(); return c.sentACK }

// VerifyO1 验证 m 个事件（合法/非法交替）的分派每次恰好检查 1 条规则、总数恰为 m。
// 只返回成败，不暴露非导出计数的数值。
func VerifyO1(m int) bool {
	c := New(100)
	start := c.checks
	for i := 0; i < m; i++ {
		before := c.checks
		e := []Event{EvData, EvACK, EvTick}[i%3]
		c.Apply(e, int64(i))
		if c.checks-before != 1 {
			return false
		}
	}
	return c.checks-start == m
}
