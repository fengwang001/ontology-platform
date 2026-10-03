// Package mirror 是影子流量镜像器的门面与记账层。
//
// 全部导出方法持有同一把互斥锁，因此并发调用等价于某个串行顺序；
// Mirror 只做内存记账，从不阻塞主路径。采样与暂停委托 sampler 包，
// 响应分类委托 diff 包。
package mirror

import (
	"fmt"
	"strings"
	"sync"

	"ontology/diff"
	"ontology/sampler"
)

// ErrKind 标识拒绝（错误）类别，检查顺序即声明顺序。
type ErrKind int

const (
	// ErrInvalidArgument 参数非法。
	ErrInvalidArgument ErrKind = iota
	// ErrInvalidTime now 不在 [0,1e15]。
	ErrInvalidTime
	// ErrClockRegression now 小于已通过检查的最大 now。
	ErrClockRegression
	// ErrDuplicate 重复（reqID 已分派 / Primary 已登记 / 镜像已 Done）。
	ErrDuplicate
	// ErrUnknown 未知（reqID 未分派 / 镜像编号未分派）。
	ErrUnknown
)

// Error 是被拒绝操作的错误，携带类别与说明。
type Error struct {
	Kind   ErrKind
	Op     string
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("mirror: %s: %s: %s", e.Op, kindNames[e.Kind], e.Detail)
}

var kindNames = map[ErrKind]string{
	ErrInvalidArgument: "参数非法",
	ErrInvalidTime:     "时间非法",
	ErrClockRegression: "时钟回退",
	ErrDuplicate:       "重复",
	ErrUnknown:         "未知",
}

// SkipReason 是 Mirror 的跳过原因；SkipNone 表示已分派。
type SkipReason int

const (
	// SkipNone 未跳过，已分派。
	SkipNone SkipReason = iota
	// SkipUnsafe 非安全方法且未允许。
	SkipUnsafe
	// SkipBodyTooLarge 请求体超过上限。
	SkipBodyTooLarge
	// SkipPaused 处于暂停期。
	SkipPaused
	// SkipNotSampled 合格但未被采中。
	SkipNotSampled
	// SkipBusy 在途数达到上限，样本丢失不补发。
	SkipBusy
)

// String 返回跳过原因的可读名称。
func (r SkipReason) String() string {
	switch r {
	case SkipNone:
		return "dispatched"
	case SkipUnsafe:
		return "unsafe"
	case SkipBodyTooLarge:
		return "body-too-large"
	case SkipPaused:
		return "paused"
	case SkipNotSampled:
		return "not-sampled"
	case SkipBusy:
		return "busy"
	}
	return "unknown"
}

// Result 是 Mirror 调用的结果。
type Result struct {
	Reason  SkipReason        // SkipNone 表示已分派
	ID      int64             // 已分派时的镜像编号（从 1 起）
	Headers map[string]string // 已分派时的镜像头（剥离敏感头并含 x-shadow: 1）
}

// Config 是镜像器构造参数。
type Config struct {
	K               int64           // 采样间隔，1..1e6
	Cm              int64           // 镜像并发上限，1..1e6
	Bm              int64           // 请求体上限，0..1e9
	E               int64           // 连续失败阈值，1..1e6
	P               int64           // 暂停时长（毫秒），1..1e9
	AllowUnsafe     bool            // 是否允许非安全方法
	SensitiveHeader map[string]bool // 敏感头名集合（小写）
	IgnoreField     map[string]bool // 忽略字段名集合
}

// Stats 是镜像器的统计快照。
type Stats struct {
	MirrorCalls       int64 // 通过检查的 Mirror 次数
	SkippedUnsafe     int64
	SkippedTooLarge   int64
	SkippedPaused     int64
	SkippedNotSampled int64
	SkippedBusy       int64
	Dispatched        int64 // 已分派数
	InFlight          int64 // 在途数
	Completed         int64 // 已完成数（Done 总数）
	CompletedOK       int64 // 已完成成功数
	MirrorErrors      int64 // 镜像错误数
	Same              int64
	Compatible        int64
	Breaking          int64
	AwaitingPrimary   int64 // 已到 Done（成功）而 Primary 未到的对数
	Qualified         int64 // 合格计数 c
	ConsecFails       int64 // 当前连续失败数 s
	PausedUntil       int64 // 暂停截止时刻
	MaxNow            int64 // 已通过检查的最大 now
}

// Mirror 是影子流量镜像器。零值不可用，须用 New 构造。
type Mirror struct {
	mu  sync.Mutex
	cfg Config
	s   *sampler.Sampler

	maxNow  int64 // 已通过检查的最大 now，初值 0
	nextID  int64 // 下一个镜像编号，从 1 起
	byReq   map[string]int64
	mirrors map[int64]*mirrorState
	stats   Stats
}

// mirrorState 记录一个已分派镜像的到达与分类状态。
type mirrorState struct {
	primary    *diff.Response // 已登记的主响应
	shadow     *diff.Response // 已到达的成功镜像响应
	done       bool           // 是否已 Done
	errored    bool           // Done 是否为错误
	classified bool           // 是否已分类
}

// New 构造 Mirror 并校验构造参数。
func New(cfg Config) (*Mirror, error) {
	bad := func(detail string) error {
		return &Error{Kind: ErrInvalidArgument, Op: "New", Detail: detail}
	}
	if cfg.K < 1 || cfg.K > 1e6 {
		return nil, bad(fmt.Sprintf("K=%d 超出 [1,1e6]", cfg.K))
	}
	if cfg.Cm < 1 || cfg.Cm > 1e6 {
		return nil, bad(fmt.Sprintf("Cm=%d 超出 [1,1e6]", cfg.Cm))
	}
	if cfg.Bm < 0 || cfg.Bm > 1e9 {
		return nil, bad(fmt.Sprintf("Bm=%d 超出 [0,1e9]", cfg.Bm))
	}
	if cfg.E < 1 || cfg.E > 1e6 {
		return nil, bad(fmt.Sprintf("E=%d 超出 [1,1e6]", cfg.E))
	}
	if cfg.P < 1 || cfg.P > 1e9 {
		return nil, bad(fmt.Sprintf("P=%d 超出 [1,1e9]", cfg.P))
	}
	s, err := sampler.New(cfg.K, cfg.E, cfg.P)
	if err != nil {
		return nil, bad(err.Error())
	}
	return &Mirror{
		cfg:     cfg,
		s:       s,
		nextID:  1,
		byReq:   make(map[string]int64),
		mirrors: make(map[int64]*mirrorState),
	}, nil
}

// Mirror 判定并可能分派一个影子镜像；从不阻塞。
func (m *Mirror) Mirror(reqID, method string, bodyLen int64, headers map[string]string, now int64) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkMirrorArgs(reqID, method, bodyLen, headers); err != nil {
		return Result{}, err
	}
	if err := m.checkTime("Mirror", now); err != nil {
		return Result{}, err
	}
	if _, ok := m.byReq[reqID]; ok {
		return Result{}, &Error{Kind: ErrDuplicate, Op: "Mirror", Detail: "reqID 已分派: " + reqID}
	}

	m.maxNow = now
	m.stats.MirrorCalls++

	if !m.cfg.AllowUnsafe && !isSafeMethod(method) {
		m.stats.SkippedUnsafe++
		return Result{Reason: SkipUnsafe}, nil
	}
	if bodyLen > m.cfg.Bm {
		m.stats.SkippedTooLarge++
		return Result{Reason: SkipBodyTooLarge}, nil
	}
	sampled, paused := m.s.Admit(now)
	if paused {
		m.stats.SkippedPaused++
		return Result{Reason: SkipPaused}, nil
	}
	if !sampled {
		m.stats.SkippedNotSampled++
		return Result{Reason: SkipNotSampled}, nil
	}
	if m.stats.InFlight >= m.cfg.Cm {
		m.stats.SkippedBusy++
		return Result{Reason: SkipBusy}, nil
	}

	id := m.nextID
	m.nextID++
	m.byReq[reqID] = id
	m.mirrors[id] = &mirrorState{}
	m.stats.Dispatched++
	m.stats.InFlight++
	return Result{Reason: SkipNone, ID: id, Headers: m.shadowHeaders(headers)}, nil
}

// Primary 登记主路径响应。
func (m *Mirror) Primary(reqID string, resp diff.Response) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if reqID == "" {
		return &Error{Kind: ErrInvalidArgument, Op: "Primary", Detail: "reqID 为空"}
	}
	id, ok := m.byReq[reqID]
	if !ok {
		return &Error{Kind: ErrUnknown, Op: "Primary", Detail: "reqID 未分派: " + reqID}
	}
	st := m.mirrors[id]
	if st.primary != nil {
		return &Error{Kind: ErrDuplicate, Op: "Primary", Detail: "reqID 已登记主响应: " + reqID}
	}
	rec := resp
	st.primary = &rec
	m.classify(st)
	return nil
}

// Done 登记镜像完成；resp 为 nil 表示镜像错误。
func (m *Mirror) Done(mirrorID int64, resp *diff.Response, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkTime("Done", now); err != nil {
		return err
	}
	st, ok := m.mirrors[mirrorID]
	if !ok {
		return &Error{Kind: ErrUnknown, Op: "Done", Detail: fmt.Sprintf("镜像编号未分派: %d", mirrorID)}
	}
	if st.done {
		return &Error{Kind: ErrDuplicate, Op: "Done", Detail: fmt.Sprintf("镜像编号已 Done: %d", mirrorID)}
	}

	m.maxNow = now
	st.done = true
	m.stats.InFlight--
	m.stats.Completed++
	if resp == nil {
		st.errored = true
		m.stats.MirrorErrors++
		m.s.OnError(now)
		return nil
	}
	rec := *resp
	st.shadow = &rec
	m.stats.CompletedOK++
	m.s.OnSuccess(now)
	m.classify(st)
	return nil
}

// Stats 返回统计快照。
func (m *Mirror) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()

	st := m.stats
	st.AwaitingPrimary = st.CompletedOK - st.Same - st.Compatible - st.Breaking
	st.Qualified = m.s.Qualified()
	st.ConsecFails = m.s.ConsecFails()
	st.PausedUntil = m.s.PausedUntil()
	st.MaxNow = m.maxNow
	return st
}

// classify 在主响应与成功镜像响应都到齐且未分类时分类一次。
func (m *Mirror) classify(st *mirrorState) {
	if st.primary == nil || st.shadow == nil || st.errored || st.classified {
		return
	}
	st.classified = true
	switch diff.Classify(*st.primary, *st.shadow, m.cfg.IgnoreField) {
	case diff.Same:
		m.stats.Same++
	case diff.Compatible:
		m.stats.Compatible++
	case diff.Breaking:
		m.stats.Breaking++
	}
}

// checkTime 校验 now 的合法性与单调性；调用方须已持锁。
func (m *Mirror) checkTime(op string, now int64) error {
	if now < 0 || now > 1e15 {
		return &Error{Kind: ErrInvalidTime, Op: op, Detail: fmt.Sprintf("now=%d 超出 [0,1e15]", now)}
	}
	if now < m.maxNow {
		return &Error{Kind: ErrClockRegression, Op: op, Detail: fmt.Sprintf("now=%d 小于最大 now=%d", now, m.maxNow)}
	}
	return nil
}

// checkMirrorArgs 校验 Mirror 的参数（不含时间）。
func (m *Mirror) checkMirrorArgs(reqID, method string, bodyLen int64, headers map[string]string) error {
	bad := func(detail string) error {
		return &Error{Kind: ErrInvalidArgument, Op: "Mirror", Detail: detail}
	}
	if reqID == "" {
		return bad("reqID 为空")
	}
	if !isKnownMethod(method) {
		return bad("未知方法: " + method)
	}
	if bodyLen < 0 || bodyLen > 1e12 {
		return bad(fmt.Sprintf("bodyLen=%d 超出 [0,1e12]", bodyLen))
	}
	seen := make(map[string]bool, len(headers))
	for name := range headers {
		if name == "" {
			return bad("头名为空")
		}
		lower := strings.ToLower(name)
		if seen[lower] {
			return bad("大小写不同的同名头: " + name)
		}
		seen[lower] = true
	}
	return nil
}

// shadowHeaders 生成镜像头：头名统一小写，剥离敏感头，覆盖 x-shadow: 1。
func (m *Mirror) shadowHeaders(headers map[string]string) map[string]string {
	out := make(map[string]string, len(headers)+1)
	for name, value := range headers {
		lower := strings.ToLower(name)
		if m.cfg.SensitiveHeader[lower] {
			continue
		}
		out[lower] = value
	}
	out["x-shadow"] = "1"
	return out
}

func isKnownMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

func isSafeMethod(method string) bool {
	return method == "GET" || method == "HEAD"
}
