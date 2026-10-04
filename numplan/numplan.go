// Package numplan 维护号码号段与号码在任一时刻的归属运营商。
package numplan

import (
	"errors"
	"sync"
)

// 全库共享的拒绝原因；portdb、route 直接复用以保证 errors.Is 跨包可用。
var (
	ErrInvalid     = errors.New("numplan: invalid argument")
	ErrClockBack   = errors.New("numplan: clock moved backwards")
	ErrNotAssigned = errors.New("numplan: number not assigned")
	ErrFrozen      = errors.New("numplan: number in disconnection freeze")
	ErrBlockExists = errors.New("numplan: block already exists")
	ErrNoOrder     = errors.New("numplan: port order not found")
	ErrEffective   = errors.New("numplan: port order already effective")
	ErrPending     = errors.New("numplan: pending port order exists")
	ErrDonor       = errors.New("numplan: donor is not current serving operator")
	ErrSameOp      = errors.New("numplan: recipient equals donor")
	ErrLeadTime    = errors.New("numplan: lead time shorter than Lmin")
)

const (
	MinDigits = 5
	MaxDigits = 15
	MaxOp     = 10000
)

// ValidNumber 报告 number 是否为 5..15 位十进制数字串。
func ValidNumber(number string) bool {
	if len(number) < MinDigits || len(number) > MaxDigits {
		return false
	}
	for i := 0; i < len(number); i++ {
		if number[i] < '0' || number[i] > '9' {
			return false
		}
	}
	return true
}

// ValidOp 报告 op 是否为 1..10^4 的运营商编号。
func ValidOp(op int) bool { return op >= 1 && op <= MaxOp }

// ValidTime 报告 t 是否为 0..10^12 的非负整数秒。
func ValidTime(t int64) bool { return t >= 0 && t <= 1e12 }

type block struct {
	prefix string
	op     int
	eff    int64
}

// Plan 是号段库，同时承载全库共享的单调时钟 maxNow。
type Plan struct {
	mu     sync.RWMutex
	maxNow int64
	// blocks[L][prefix]：按号码总长分桶。
	blocks map[int]map[string]block
}

// New 创建号段库。Lmin、Q 由 portdb.New 接收。
func New() *Plan {
	return &Plan{blocks: make(map[int]map[string]block)}
}

// Sync 暴露内部锁；上层包（portdb、route）按 route→portdb→numplan 顺序持锁。
func (p *Plan) Sync() *sync.RWMutex { return &p.mu }

// MaxNow 返回已接受的最大 now（调用方须自行持锁或接受近似值）。
func (p *Plan) MaxNow() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.maxNow
}

// MaxNowLocked 在已持有 p.Sync() 时读取全局时钟。
func (p *Plan) MaxNowLocked() int64 { return p.maxNow }

// Probe 记录一次求值实际考察的记录数，用于复杂度探针。
type Probe struct {
	Blocks  int
	History int
}

func (p *Plan) AssignBlock(prefix string, length int, op int, now int64) error {
	if length < MinDigits || length > MaxDigits ||
		len(prefix) < 1 || len(prefix) > length ||
		!digits(prefix) || !ValidOp(op) || !ValidTime(now) {
		return ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.maxNow {
		return ErrClockBack
	}
	bucket := p.blocks[length]
	if bucket != nil {
		if _, exists := bucket[prefix]; exists {
			return ErrBlockExists
		}
	} else {
		bucket = make(map[string]block)
		p.blocks[length] = bucket
	}
	bucket[prefix] = block{prefix: prefix, op: op, eff: now}
	p.maxNow = now
	return nil
}

// HomeAtLocked 返回 number 在 t 时刻的归属运营商；未分配时 ok=false。
// 调用方必须持有 p.Sync() 的读锁或写锁。
func (p *Plan) HomeAtLocked(number string, t int64) (op int, ok bool, pr Probe) {
	bucket := p.blocks[len(number)]
	if bucket == nil {
		return 0, false, Probe{}
	}
	// 自最长前缀向短探测；同一总长下前缀互不为真前缀，命中即唯一最长。
	for k := len(number); k >= 1; k-- {
		b, exists := bucket[number[:k]]
		if exists {
			pr.Blocks++
			if b.eff <= t {
				return b.op, true, pr
			}
		}
	}
	return 0, false, pr
}

func (p *Plan) HomeAt(number string, t int64) (int, bool, Probe) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.HomeAtLocked(number, t)
}

// CheckTimeLocked 在已持写锁的情况下校验 now 不早于全局时钟，并推进时钟。
func (p *Plan) CheckTimeLocked(now int64) error {
	if now < p.maxNow {
		return ErrClockBack
	}
	p.maxNow = now
	return nil
}

// PeekTimeLocked 只读校验 t 不晚于全局时钟（供 QueryAt）。
func (p *Plan) PeekTimeLocked(t int64) error {
	if t > p.maxNow {
		return ErrInvalid
	}
	return nil
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
