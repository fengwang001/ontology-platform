package certcache

import (
	"io"
	"log"
	"os"
	"sort"
	"sync"
	"time"
)

// Status 为证书状态响应所声明的状态。
type Status string

const (
	// StatusGood 正常。
	StatusGood Status = "good"
	// StatusRevoked 吊销（终态）。
	StatusRevoked Status = "revoked"
	// StatusSuspended 暂扣。
	StatusSuspended Status = "suspended"
)

// Response 为一条证书状态响应。
type Response struct {
	Serial string    // 证书序号
	Status Status    // 状态：good / suspended / revoked
	A      time.Time // 生效时刻
	B      time.Time // 下次更新时刻
}

// Record 为缓存内每个证书唯一的合并记录。
type Record struct {
	Serial string
	Status Status
	A      time.Time
	B      time.Time
}

// RejectReason 描述响应被拒绝的可区分原因。
type RejectReason string

const (
	RejectEmptySerial RejectReason = "empty_serial"
	RejectBadStatus   RejectReason = "bad_status"
	RejectBadInterval RejectReason = "bad_interval"
	RejectFuture      RejectReason = "future_response"
	RejectCacheFull   RejectReason = "cache_full"
)

// SubmitResult 为响应提交结果。
type SubmitResult struct {
	Accepted bool
	Reason   RejectReason
	Record   Record
}

// TrustReason 为当前时刻信任判定的结论。
type TrustReason string

const (
	TrustUnknown   TrustReason = "unknown"
	TrustRevoked   TrustReason = "revoked"
	TrustSuspended TrustReason = "suspended"
	TrustExpired   TrustReason = "expired"
	TrustGood      TrustReason = "good"
)

// Verdict 为一次信任判定的结果。
type Verdict struct {
	Serial    string
	Trusted   bool
	Reason    TrustReason
	At        time.Time
	Record    Record
	HasRecord bool
}

// String 返回拒绝原因的中文描述。
func (r RejectReason) String() string {
	switch r {
	case RejectEmptySerial:
		return "证书序号为空"
	case RejectBadStatus:
		return "状态非法"
	case RejectBadInterval:
		return "生效时刻不早于下次更新时刻(a>=b)"
	case RejectFuture:
		return "响应来自未来(a晚于当前时刻)"
	case RejectCacheFull:
		return "缓存容量已满且无可淘汰的过期正常记录"
	default:
		return string(r)
	}
}

// String 返回判定结论的中文描述。
func (t TrustReason) String() string {
	switch t {
	case TrustUnknown:
		return "未知"
	case TrustRevoked:
		return "已吊销"
	case TrustSuspended:
		return "已暂扣"
	case TrustExpired:
		return "状态已过期"
	case TrustGood:
		return "可信"
	default:
		return string(t)
	}
}

// Cache 为证书吊销状态响应缓存。
//
// 所有读写均在同一把互斥锁下完成，因此 Submit 与 Trust 可被并发调用，
// 且相同输入序列在相同时钟下产生完全相同的结果。
type Cache struct {
	capacity int
	now      func() time.Time

	mu           sync.RWMutex
	records      map[string]*Record
	revokedLatch bool

	logf func(format string, args ...any)
}

// New 创建容量为 M 的缓存（M<=0 时任何新证书都无法进入缓存）。
func New(capacity int) *Cache {
	return NewWithClock(capacity, time.Now)
}

// NewWithClock 使用可注入的时钟创建缓存，便于确定性测试。
func NewWithClock(capacity int, now func() time.Time) *Cache {
	logger := log.New(os.Stderr, "certcache ", log.LstdFlags|log.Lmicroseconds)
	return &Cache{
		capacity: capacity,
		now:      now,
		records:  make(map[string]*Record),
		logf:     logger.Printf,
	}
}

// SetLogOutput 重定向合并与判定日志（传 io.Discard 可静默）。
func (c *Cache) SetLogOutput(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logf = log.New(w, "certcache ", log.LstdFlags|log.Lmicroseconds).Printf
}

// Len 返回当前缓存的证书数量。
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.records)
}

// Snapshot 返回按序号排序的全部记录副本。
func (c *Cache) Snapshot() []Record {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Record, 0, len(c.records))
	for _, rec := range c.records {
		out = append(out, *rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Serial < out[j].Serial })
	return out
}

func validStatus(s Status) bool {
	return s == StatusGood || s == StatusRevoked || s == StatusSuspended
}

// Submit 按固定顺序校验并合并一条状态响应。被拒绝时不改变任何记录。
func (c *Cache) Submit(resp Response) SubmitResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()

	// 校验顺序：序号 -> 状态 -> 区间 -> 未来时刻 -> 容量，只报第一个原因。
	if resp.Serial == "" {
		return c.reject(resp, now, RejectEmptySerial)
	}
	if !validStatus(resp.Status) {
		return c.reject(resp, now, RejectBadStatus)
	}
	if !resp.A.Before(resp.B) {
		return c.reject(resp, now, RejectBadInterval)
	}
	if resp.A.After(now) {
		return c.reject(resp, now, RejectFuture)
	}

	existing := c.records[resp.Serial]
	if existing == nil && len(c.records) >= c.capacity {
		if victim := c.pickEvictVictim(now); victim != nil {
			delete(c.records, victim.Serial)
			c.logf("输入 submit serial=%s status=%s a=%s b=%s @%s | 淘汰 serial=%s b=%s(过期正常记录,b最小;并列取序号小)",
				resp.Serial, resp.Status, resp.A.Format(time.RFC3339Nano), resp.B.Format(time.RFC3339Nano),
				now.Format(time.RFC3339Nano), victim.Serial, victim.B.Format(time.RFC3339Nano))
		} else {
			return c.reject(resp, now, RejectCacheFull)
		}
	}

	merged, basis := mergeRecord(existing, resp)
	c.records[merged.Serial] = &merged

	c.logf("输入 submit serial=%s status=%s a=%s b=%s @%s | 输出 accepted record={%s %s a=%s b=%s} | 依据 %s",
		resp.Serial, resp.Status, resp.A.Format(time.RFC3339Nano), resp.B.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
		merged.Serial, merged.Status, merged.A.Format(time.RFC3339Nano), merged.B.Format(time.RFC3339Nano), basis)

	return SubmitResult{Accepted: true, Record: merged}
}

func (c *Cache) reject(resp Response, now time.Time, reason RejectReason) SubmitResult {
	c.logf("输入 submit serial=%q status=%s a=%s b=%s @%s | 输出 rejected reason=%s:%s | 依据 按固定校验顺序命中第一个原因,任何记录不变",
		resp.Serial, resp.Status, fmtTime(resp.A), fmtTime(resp.B), now.Format(time.RFC3339Nano),
		reason, reason.String())
	return SubmitResult{Accepted: false, Reason: reason}
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format(time.RFC3339Nano)
}

// pickEvictVictim 在已过期(now>=b)的正常记录中选 b 最小者，并列取序号字典序最小者。
func (c *Cache) pickEvictVictim(now time.Time) *Record {
	var victim *Record
	for _, rec := range c.records {
		if rec.Status != StatusGood || now.Before(rec.B) {
			continue
		}
		if victim == nil ||
			rec.B.Before(victim.B) ||
			(rec.B.Equal(victim.B) && rec.Serial < victim.Serial) {
			victim = rec
		}
	}
	return victim
}

// mergeRecord 将新响应并入已有记录，返回新记录与判定依据。
// 规则满足交换律与结合律，因此合并结果与到达顺序无关。
func mergeRecord(existing *Record, resp Response) (Record, string) {
	incoming := Record{Serial: resp.Serial, Status: resp.Status, A: resp.A, B: resp.B}
	if existing == nil {
		return incoming, "该证书首条响应,直接建立记录"
	}

	merged := *existing
	switch {
	case existing.Status == StatusRevoked && resp.Status == StatusRevoked:
		// 同为吊销：取最小 a；a 相同取最小 b（b 仅用于保证记录与顺序无关）。
		if resp.A.Before(existing.A) || (resp.A.Equal(existing.A) && resp.B.Before(existing.B)) {
			merged.A, merged.B = resp.A, resp.B
			return merged, "吊销终态下取更早生效(a最小,并列b最小)的吊销响应"
		}
		return merged, "吊销为终态,该吊销响应不更早,忽略"
	case existing.Status == StatusRevoked:
		return merged, "吊销为终态,非吊销响应一律忽略,已吊销证书不恢复信任"
	case resp.Status == StatusRevoked:
		return incoming, "吊销为终态,立即覆盖此前正常/暂扣记录"
	default:
		// 双方均非吊销：a 大者胜；a 等暂扣胜正常；状态与 a 皆等取较小 b。
		switch {
		case resp.A.After(existing.A):
			merged = incoming
			return merged, "新响应a更大(更新),覆盖旧记录"
		case resp.A.Before(existing.A):
			return merged, "已有记录a更大(更新),忽略较旧响应"
		case existing.Status == StatusSuspended && resp.Status == StatusGood:
			return merged, "a相同,暂扣优先于正常,保留暂扣"
		case existing.Status == StatusGood && resp.Status == StatusSuspended:
			merged = incoming
			return merged, "a相同,暂扣优先于正常,采用暂扣"
		case resp.B.Before(existing.B):
			merged.B = resp.B
			return merged, "状态与a均相同,取较小b,记录b取胜出响应自身的b"
		default:
			return merged, "状态与a均相同且b不更小,记录不变"
		}
	}
}

// Trust 判定证书在当前时刻是否可信。
func (c *Cache) Trust(serial string) Verdict {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	v := Verdict{Serial: serial, At: now}

	// 全局吊销闩锁：任一判定一旦报出已吊销，其后所有判定一律不可信。
	if c.revokedLatch {
		v.Reason = TrustRevoked
		c.logf("输入 trust serial=%s @%s | 输出 trusted=false reason=%s:%s | 依据 全局吊销闩锁已触发(任一证书已被判定吊销),所有判定不可信",
			serial, now.Format(time.RFC3339Nano), v.Reason, v.Reason.String())
		return v
	}

	rec, ok := c.records[serial]
	if !ok {
		v.Reason = TrustUnknown
		c.logf("输入 trust serial=%s @%s | 输出 trusted=false reason=%s:%s | 依据 缓存中无该证书记录",
			serial, now.Format(time.RFC3339Nano), v.Reason, v.Reason.String())
		return v
	}

	v.Record, v.HasRecord = *rec, true
	switch rec.Status {
	case StatusRevoked:
		// 任一判定一旦报出已吊销即置闩锁：此后所有判定一律不可信。
		c.revokedLatch = true
		v.Reason = TrustRevoked
	case StatusSuspended:
		v.Reason = TrustSuspended
	case StatusGood:
		if now.Before(rec.B) {
			v.Reason = TrustGood
			v.Trusted = true
		} else {
			v.Reason = TrustExpired
		}
	}

	c.logf("输入 trust serial=%s @%s | 输出 trusted=%t reason=%s:%s record={%s %s a=%s b=%s} | 依据 %s",
		serial, now.Format(time.RFC3339Nano), v.Trusted, v.Reason, v.Reason.String(),
		rec.Serial, rec.Status, rec.A.Format(time.RFC3339Nano), rec.B.Format(time.RFC3339Nano),
		trustBasis(v.Reason))
	return v
}

func trustBasis(reason TrustReason) string {
	switch reason {
	case TrustRevoked:
		return "记录为吊销终态,任何时刻不可信"
	case TrustSuspended:
		return "记录为暂扣,不可信"
	case TrustExpired:
		return "正常记录但当前时刻>=b,有效区间[a,b)已结束(恰在b时刻即过期)"
	case TrustGood:
		return "正常记录且当前时刻<b,处于有效区间[a,b)"
	default:
		return "无记录"
	}
}
