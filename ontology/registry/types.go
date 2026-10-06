package registry

import (
	"fmt"
	"io"
	"sync"
)

// ErrorCode 为可区分的拒绝类别。拒绝判定次序按数值从小到大。
type ErrorCode int

const (
	CodeInvalidParam         ErrorCode = iota // 参数非法
	CodeOutsideQualification                  // 资格外
	CodeConflictIssued                        // 与已核发冲突
	CodeStateNotAllowed                       // 状态不允许
	CodeNotHolder                             // 非持有人
	CodePeriodMismatch                        // 期限不符
	CodeOverConsumption                       // 超出用电量
	CodeBelowCancelled                        // 低于已注销量
)

// Error 为一次被拒绝操作携带类别、可读原因与（批内操作时）首个失败序号。
type Error struct {
	Code   ErrorCode
	Serial int64 // 批内首个失败证书序号；无序号上下文时为 0
	Reason string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Serial != 0 {
		return fmt.Sprintf("%s: serial=%d: %s", codeName(e.Code), e.Serial, e.Reason)
	}
	return fmt.Sprintf("%s: %s", codeName(e.Code), e.Reason)
}

func codeName(c ErrorCode) string {
	switch c {
	case CodeInvalidParam:
		return "参数非法"
	case CodeOutsideQualification:
		return "资格外"
	case CodeConflictIssued:
		return "与已核发冲突"
	case CodeStateNotAllowed:
		return "状态不允许"
	case CodeNotHolder:
		return "非持有人"
	case CodePeriodMismatch:
		return "期限不符"
	case CodeOverConsumption:
		return "超出用电量"
	case CodeBelowCancelled:
		return "低于已注销量"
	default:
		return "未知错误"
	}
}

func errAt(code ErrorCode, serial int64, format string, args ...any) *Error {
	return &Error{Code: code, Serial: serial, Reason: fmt.Sprintf(format, args...)}
}

func errf(code ErrorCode, format string, args ...any) *Error {
	return errAt(code, 0, format, args...)
}

// Status 为证书状态。
type Status uint8

const (
	StatusHeld      Status = iota // 持有
	StatusCancelled               // 已注销
	StatusRevoked                 // 已撤销
)

func (s Status) String() string {
	switch s {
	case StatusHeld:
		return "held"
	case StatusCancelled:
		return "cancelled"
	case StatusRevoked:
		return "revoked"
	default:
		return "unknown"
	}
}

// Cert 为一张绿电证书的完整状态。
type Cert struct {
	Serial    int64
	Facility  string
	Period    int64 // 发电期
	Holder    string
	Status    Status
	Consumer  string // 注销到的用电方（仅已注销/被撤销的注销证书）
	UsePeriod int64  // 注销到的用电期
	CancelSeq int64  // 注销时刻序号（晚注销者更大）
}

// EventKind 标识登记簿事件类型。
type EventKind uint8

const (
	EventMeterRegistered EventKind = iota
	EventIssued
	EventTransferred
	EventCancelled
	EventRevokedHeld
	EventDeclarationInvalidated
	EventQualEndSet
	EventQualEndRevoked
	EventConsumptionRegistered
)

// Event 为一次成功操作产生的可重放事件。
type Event struct {
	Kind      EventKind
	Facility  string
	Period    int64
	Holder    string
	Consumer  string
	ToHolder  string
	UsePeriod int64
	Serial    int64
	Serials   []int64
	Qty       int64
	Remainder int64
	EndPeriod int64
}

// Config 为登记簿配置。
type Config struct {
	UnitQty       int64 // 每张证书对应电量（整数单位），须为正
	MaxAgePeriods int64 // 发电期最早可早于用电期的期数，取等允许，须非负
	MinPeriod     int64 // 合法期编号下界（含）
	MaxPeriod     int64 // 合法期编号上界（含）
}

// heldHeap 为某设施某发电期“持有中”证书的最大序号堆，惰性删除。
type heldHeap struct {
	serials []int64
	present map[int64]struct{}
}

func newHeldHeap() *heldHeap {
	return &heldHeap{present: map[int64]struct{}{}}
}

func (h *heldHeap) push(serial int64) {
	h.present[serial] = struct{}{}
	h.serials = append(h.serials, serial)
	h.up(len(h.serials) - 1)
}

func (h *heldHeap) remove(serial int64) {
	delete(h.present, serial)
}

// popMax 取出并删除仍处于持有集合中的最大序号；集合空时返回 0。
func (h *heldHeap) popMax() int64 {
	for len(h.serials) > 0 {
		top := h.serials[0]
		n := len(h.serials)
		h.serials[0] = h.serials[n-1]
		h.serials = h.serials[:n-1]
		if n > 1 {
			h.down(0)
		}
		if _, ok := h.present[top]; ok {
			delete(h.present, top)
			return top
		}
	}
	return 0
}

func (h *heldHeap) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if h.serials[p] >= h.serials[i] {
			return
		}
		h.serials[p], h.serials[i] = h.serials[i], h.serials[p]
		i = p
	}
}

func (h *heldHeap) down(i int) {
	n := len(h.serials)
	for {
		l, r, best := 2*i+1, 2*i+2, i
		if l < n && h.serials[l] > h.serials[best] {
			best = l
		}
		if r < n && h.serials[r] > h.serials[best] {
			best = r
		}
		if best == i {
			return
		}
		h.serials[i], h.serials[best] = h.serials[best], h.serials[i]
		i = best
	}
}

// cancelRec 记录一张已注销证书，按注销时刻追加；尾即最晚注销。
type cancelRec struct {
	Serial    int64
	CancelSeq int64
}

// facPeriod 为某设施某发电期的计量、余量与证书索引。
type facPeriod struct {
	qty       int64 // 最新登记电量
	inRem     int64 // 核发前并入的账户余量
	outRem    int64 // 核发后新的账户余量
	held      *heldHeap
	cancelled []cancelRec
	issued    int // 该期曾核发张数
	active    int // 当前非撤销张数（持有 + 已注销）
}

type facility struct {
	id        string
	holder    string
	start     int64
	end       int64 // 0 表示开放终止
	rem       int64 // 当前账户余量
	maxIssued int64 // 曾核发证书的最大发电期
	periods   map[int64]*facPeriod
}

type conPeriod struct {
	qty       int64 // 登记用电量；未登记为 -1
	cancelled int64 // 当前有效注销量
}

type consumer struct {
	periods map[int64]*conPeriod
}

// Registry 为绿电证书登记簿。所有方法可并发调用。
type Registry struct {
	mu     sync.Mutex
	cfg    Config
	certs  map[int64]*Cert
	facs   map[string]*facility
	cons   map[string]*consumer
	serial int64 // 已分配最大序号（连续无洞）
	cseq   int64 // 注销时刻计数器
	events []Event
	trace  io.Writer
}
