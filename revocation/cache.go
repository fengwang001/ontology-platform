// Package revocation 实现证书吊销状态响应缓存。
//
// 缓存合并乱序到达的状态响应，并按有效区间 [a, b) 判定证书当前是否可信。
// 合并结果与响应到达顺序无关：吊销为终态，一旦记录为吊销，
// 仅更早生效（a 更小）的吊销响应会把生效时刻改小，其余响应均被忽略。
package revocation

import (
	"fmt"
	"sync"
	"time"
)

// Status 表示证书状态。
type Status int

const (
	// StatusGood 正常。
	StatusGood Status = iota
	// StatusSuspended 暂扣。
	StatusSuspended
	// StatusRevoked 吊销（终态）。
	StatusRevoked
)

func (s Status) String() string {
	switch s {
	case StatusGood:
		return "正常"
	case StatusSuspended:
		return "暂扣"
	case StatusRevoked:
		return "吊销"
	default:
		return fmt.Sprintf("非法状态(%d)", int(s))
	}
}

func (s Status) valid() bool {
	return s == StatusGood || s == StatusSuspended || s == StatusRevoked
}

// Response 是一条证书状态响应：状态在 [A, B) 区间内有效。
type Response struct {
	Serial string    // 证书序号
	Status Status    // 状态
	A      time.Time // 生效时刻
	B      time.Time // 下次更新时刻（A 必须小于 B）
}

func (r Response) String() string {
	return fmt.Sprintf("{序号:%s 状态:%s a:%s b:%s}",
		r.Serial, r.Status, r.A.Format(time.RFC3339Nano), r.B.Format(time.RFC3339Nano))
}

// Record 是缓存中为每个证书保留的唯一合并记录。
type Record struct {
	Status Status
	A      time.Time
	B      time.Time
}

func (r Record) String() string {
	return fmt.Sprintf("{状态:%s a:%s b:%s}",
		r.Status, r.A.Format(time.RFC3339Nano), r.B.Format(time.RFC3339Nano))
}

// RejectReason 区分响应被拒绝的原因。
type RejectReason int

const (
	// RejectEmptySerial 证书序号为空。
	RejectEmptySerial RejectReason = iota
	// RejectInvalidStatus 状态非法。
	RejectInvalidStatus
	// RejectInvalidInterval a 不小于 b。
	RejectInvalidInterval
	// RejectFutureResponse a 晚于当前时刻（来自未来）。
	RejectFutureResponse
	// RejectCacheFull 容量已满且无可淘汰记录。
	RejectCacheFull
)

func (r RejectReason) String() string {
	switch r {
	case RejectEmptySerial:
		return "证书序号为空"
	case RejectInvalidStatus:
		return "状态非法"
	case RejectInvalidInterval:
		return "生效时刻不小于下次更新时刻"
	case RejectFutureResponse:
		return "响应来自未来"
	case RejectCacheFull:
		return "缓存容量已满且无可淘汰记录"
	default:
		return fmt.Sprintf("未知拒绝原因(%d)", int(r))
	}
}

// RejectError 表示响应被整体拒绝，Reason 给出第一个拒绝原因。
type RejectError struct {
	Reason RejectReason
	Serial string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("响应被拒绝（序号:%q）：%s", e.Serial, e.Reason)
}

// Verdict 是判定结果。
type Verdict int

const (
	// VerdictUnknown 无记录，不可信。
	VerdictUnknown Verdict = iota
	// VerdictRevoked 已吊销，不可信。
	VerdictRevoked
	// VerdictSuspended 已暂扣，不可信。
	VerdictSuspended
	// VerdictExpired 正常记录已过期，不可信。
	VerdictExpired
	// VerdictGood 正常且未过期，可信。
	VerdictGood
)

func (v Verdict) String() string {
	switch v {
	case VerdictUnknown:
		return "未知"
	case VerdictRevoked:
		return "已吊销"
	case VerdictSuspended:
		return "已暂扣"
	case VerdictExpired:
		return "状态已过期"
	case VerdictGood:
		return "可信"
	default:
		return fmt.Sprintf("非法判定(%d)", int(v))
	}
}

// Trusted 报告该判定是否表示证书可信。
func (v Verdict) Trusted() bool { return v == VerdictGood }

// Cache 是证书吊销状态响应缓存，可被并发调用。
type Cache struct {
	mu      sync.Mutex
	max     int
	now     func() time.Time
	records map[string]Record
}

// Option 配置缓存。
type Option func(*Cache)

// WithClock 注入时钟（用于测试），默认为 time.Now。
func WithClock(now func() time.Time) Option {
	return func(c *Cache) { c.now = now }
}

// New 创建最多容纳 max 个证书的缓存。
func New(max int, opts ...Option) *Cache {
	if max < 0 {
		max = 0
	}
	c := &Cache{max: max, now: time.Now, records: make(map[string]Record)}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Submit 校验并合并一条状态响应。
// 校验顺序：序号为空、状态非法、a 不小于 b、a 来自未来、容量已满，
// 只报第一个原因；被拒绝的响应不改变任何记录。
func (c *Cache) Submit(resp Response) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if resp.Serial == "" {
		return &RejectError{Reason: RejectEmptySerial, Serial: resp.Serial}
	}
	if !resp.Status.valid() {
		return &RejectError{Reason: RejectInvalidStatus, Serial: resp.Serial}
	}
	if !resp.A.Before(resp.B) {
		return &RejectError{Reason: RejectInvalidInterval, Serial: resp.Serial}
	}
	if resp.A.After(now) {
		return &RejectError{Reason: RejectFutureResponse, Serial: resp.Serial}
	}

	rec, exists := c.records[resp.Serial]
	if !exists {
		if len(c.records) >= c.max && !c.evictOneLocked(now) {
			return &RejectError{Reason: RejectCacheFull, Serial: resp.Serial}
		}
		c.records[resp.Serial] = Record{Status: resp.Status, A: resp.A, B: resp.B}
		return nil
	}

	c.records[resp.Serial] = merge(rec, resp)
	return nil
}

// merge 将响应合并进已有记录，结果与到达顺序无关。
func merge(rec Record, resp Response) Record {
	if rec.Status == StatusRevoked {
		// 吊销为终态：仅更早生效的吊销响应把 a 改小。
		if resp.Status == StatusRevoked && resp.A.Before(rec.A) {
			return Record{Status: StatusRevoked, A: resp.A, B: resp.B}
		}
		return rec
	}
	if resp.Status == StatusRevoked {
		return Record{Status: StatusRevoked, A: resp.A, B: resp.B}
	}
	// 双方均非吊销：a 较大者胜；a 相等暂扣胜正常；
	// 状态与 a 都相同取较小的 b；b 取自身。
	if resp.A.After(rec.A) {
		return Record{Status: resp.Status, A: resp.A, B: resp.B}
	}
	if resp.A.Before(rec.A) {
		return rec
	}
	if resp.Status != rec.Status {
		if resp.Status == StatusSuspended {
			return Record{Status: resp.Status, A: resp.A, B: resp.B}
		}
		return rec
	}
	if resp.B.Before(rec.B) {
		return Record{Status: resp.Status, A: resp.A, B: resp.B}
	}
	return rec
}

// evictOneLocked 淘汰一条已过期的正常记录（b 最小者，并列取序号字典序小）。
// 返回是否成功淘汰。
func (c *Cache) evictOneLocked(now time.Time) bool {
	victim := ""
	var victimRec Record
	for serial, rec := range c.records {
		if rec.Status != StatusGood || now.Before(rec.B) {
			continue // 只淘汰已过期的正常记录
		}
		if victim == "" || rec.B.Before(victimRec.B) ||
			(rec.B.Equal(victimRec.B) && serial < victim) {
			victim, victimRec = serial, rec
		}
	}
	if victim == "" {
		return false
	}
	delete(c.records, victim)
	return true
}

// Judge 判定证书在当前时刻是否可信。
func (c *Cache) Judge(serial string) Verdict {
	c.mu.Lock()
	defer c.mu.Unlock()

	rec, ok := c.records[serial]
	if !ok {
		return VerdictUnknown
	}
	switch rec.Status {
	case StatusRevoked:
		return VerdictRevoked
	case StatusSuspended:
		return VerdictSuspended
	default:
		if c.now().Before(rec.B) {
			return VerdictGood
		}
		return VerdictExpired
	}
}

// Record 返回证书的合并记录及是否存在（供测试与诊断）。
func (c *Cache) Record(serial string) (Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.records[serial]
	return rec, ok
}

// Len 返回当前缓存的证书数量。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.records)
}
