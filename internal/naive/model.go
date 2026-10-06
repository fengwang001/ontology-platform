// Package naive 是独立编写的朴素参考模型：单把互斥锁保护全部状态，
// 不追求并发性能，只追求规则显而易见地正确，供差分测试对照生产实现。
package naive

import "sync"

type Status int

const (
	StatusCreated Status = iota
	StatusShipped
	StatusClosed
	StatusCancelled
)

type Line struct {
	Item string
	Qty  int64
}

type LineState struct {
	Item      string
	Requested int64
	Issued    int64
	Received  int64
	Shortage  int64
	Overage   int64
}

type order struct {
	id, source, dest string
	status           Status
	shipTime         int64
	lines            []LineState
}

type Model struct {
	mu        sync.Mutex
	now       int64
	tolerance int64
	wait      int64
	initial   map[string]int64
	avail     map[string]map[string]int64 // 仓 → 商品 → 可用
	frozen    map[string]map[string]int64 // 仓 → 商品 → 冻结
	orders    map[string]*order
}

func key(wh, item string) string { return wh + "\x00" + item }

func New(tolerancePermille, waitSeconds int64, initial map[string]map[string]int64) *Model {
	m := &Model{
		tolerance: tolerancePermille,
		wait:      waitSeconds,
		initial:   map[string]int64{},
		avail:     map[string]map[string]int64{},
		frozen:    map[string]map[string]int64{},
		orders:    map[string]*order{},
	}
	for wh, items := range initial {
		for item, qty := range items {
			m.initial[item] += qty
			if m.avail[wh] == nil {
				m.avail[wh] = map[string]int64{}
			}
			m.avail[wh][item] += qty
		}
	}
	return m
}

func (m *Model) a(wh, item string) int64 { return m.avail[wh][item] }
func (m *Model) f(wh, item string) int64 { return m.frozen[wh][item] }

func (m *Model) addAvail(wh, item string, d int64) {
	if m.avail[wh] == nil {
		m.avail[wh] = map[string]int64{}
	}
	m.avail[wh][item] += d
}

func (m *Model) addFrozen(wh, item string, d int64) {
	if m.frozen[wh] == nil {
		m.frozen[wh] = map[string]int64{}
	}
	m.frozen[wh][item] += d
}

type Outcome struct {
	Err error
}

type errCode string

const (
	CodeInvalidArgument   errCode = "invalid_argument"
	CodeClockRollback     errCode = "clock_rollback"
	CodeNotFound          errCode = "not_found"
	CodeInvalidState      errCode = "invalid_state"
	CodeInsufficientStock errCode = "insufficient_stock"
	CodeOverReceipt       errCode = "over_receipt"
	CodeCloseTooEarly     errCode = "close_too_early"
	CodeRecoveryExceed    errCode = "recovery_exceed"
	CodeNoShortage        errCode = "no_shortage"
)

type Err struct {
	Code  errCode
	State Status
}

func (e *Err) Error() string { return string(e.Code) }

func ec(code errCode) error { return &Err{Code: code} }
func ecState(code errCode, st Status) error {
	return &Err{Code: code, State: st}
}
