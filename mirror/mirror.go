package mirror

import (
	"errors"
	"sync"

	"ontology/diff"
	"ontology/sampler"
)

// 跳过原因，与 sampler.SkipReason 数值对齐。
const (
	SkipUnsafe = iota + 1
	SkipBodyTooLarge
	SkipPaused
	SkipNotSelected
	SkipBusy
)

const maxNowBound int64 = 1_000_000_000_000_000

// Config 是镜像器的构造参数。
type Config struct {
	K           int64
	Cm          int
	Bm          int64
	E           int
	P           int64
	AllowUnsafe bool
	Sensitive   map[string]struct{} // 敏感头名（小写）
	Ignore      map[string]struct{} // 忽略字段名
}

// Skip 是一次 Mirror 调用的非分派结果（跳过不是错误）。
type Skip struct {
	Reason int
}

// Dispatch 是一次 Mirror 调用的分派结果。
type Dispatch struct {
	MirrorID int64
	Headers  map[string]string // 已剥离敏感头并统一小写、含 x-shadow: 1 的副本
}

// Stats 是可精确复现的记账与分类统计。
type Stats struct {
	Accepted       int64 // 通过检查的 Mirror 次数
	SkippedUnsafe  int64
	SkippedBody    int64
	SkippedPause   int64
	SkippedSample  int64 // 未采中（= c - floor(c/k)）
	SkippedBusy    int64
	Dispatched     int64
	InFlight       int
	Completed      int64 // 已 Done 的镜像数（含错误）
	ShadowErrors   int64
	Identical      int64
	Compatible     int64
	Breaking       int64
	PendingPrimary int64 // 已到成功 Done 而 Primary 未到
	PendingDone    int64 // 已到 Primary 而成功 Done 未到（错误 Done 不计）
	PausedUntil    int64
}

var (
	ErrInvalid   = errors.New("mirror: invalid argument")
	ErrTime      = errors.New("mirror: invalid time")
	ErrClock     = errors.New("mirror: clock went backwards")
	ErrDuplicate = errors.New("mirror: duplicate")
	ErrUnknown   = errors.New("mirror: unknown")
)

var validMethods = map[string]struct{}{
	"GET": {}, "HEAD": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {},
}

// slot 是一个已分派请求的配对槽，同时挂在请求表与镜像表上。
type slot struct {
	reqID        string
	mirrorID     int64
	doneErr      bool
	doneOK       bool
	primaryFirst bool // Primary 先于成功 Done 到达（用于冲销半到计数）
	shadowResp   diff.Response
	primaryOK    bool
	primaryResp  diff.Response
}

// M 是影子流量镜像器，所有方法可并发调用，语义等价于某一串行顺序。
type M struct {
	mu       sync.RWMutex
	smp      *sampler.Sampler
	cfg      Config
	reqs     map[string]*slot
	ids      map[int64]*slot
	nextID   int64
	inFlight int

	accepted       int64
	skipUnsafe     int64
	skipBody       int64
	skipPause      int64
	skipSample     int64
	skipBusy       int64
	dispatched     int64
	completed      int64
	shadowErrors   int64
	identical      int64
	compatible     int64
	breaking       int64
	pendingPrimary int64
	pendingDone    int64
}

// New 校验参数并构造镜像器。
func New(cfg Config) (*M, error) {
	smp, err := sampler.New(sampler.Config{
		K: cfg.K, Cm: cfg.Cm, Bm: cfg.Bm, E: cfg.E, P: cfg.P, AllowUnsafe: cfg.AllowUnsafe,
	})
	if err != nil {
		return nil, ErrInvalid
	}
	return &M{
		smp:  smp,
		cfg:  cfg,
		reqs: map[string]*slot{},
		ids:  map[int64]*slot{},
	}, nil
}

// Mirror 判定一次生产请求是否镜像；绝不阻塞、绝不 panic，不影响主路径。
func (m *M) Mirror(reqID, method string, bodyLen int64, headers map[string]string, now int64) (*Dispatch, *Skip, error) {
	// 参数检查（在触碰任何状态之前）。
	if reqID == "" {
		return nil, nil, ErrInvalid
	}
	if _, ok := validMethods[method]; !ok {
		return nil, nil, ErrInvalid
	}
	if bodyLen < 0 || bodyLen > 1_000_000_000_000 {
		return nil, nil, ErrInvalid
	}
	norm, err := normalizeHeaders(headers)
	if err != nil {
		return nil, nil, err
	}
	if now < 0 || now > maxNowBound {
		return nil, nil, ErrTime
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.smp.CheckClock(now); err != nil {
		if errors.Is(err, sampler.ErrClockBack) {
			return nil, nil, ErrClock
		}
		return nil, nil, ErrTime
	}
	if _, dup := m.reqs[reqID]; dup {
		return nil, nil, ErrDuplicate
	}

	m.accepted++
	reason, selected := m.smp.Filter(method, bodyLen, now, m.inFlight)
	if !selected {
		switch reason {
		case sampler.Unsafe:
			m.skipUnsafe++
		case sampler.BodyTooLarge:
			m.skipBody++
		case sampler.Paused:
			m.skipPause++
		case sampler.NotSelected:
			m.skipSample++
		case sampler.Busy:
			m.skipBusy++
		}
		return nil, &Skip{Reason: int(reason)}, nil
	}

	m.nextID++
	id := m.nextID
	m.inFlight++
	m.dispatched++
	sl := &slot{reqID: reqID, mirrorID: id}
	m.reqs[reqID] = sl
	m.ids[id] = sl

	out := stripHeaders(norm, m.cfg.Sensitive)
	out["x-shadow"] = "1"
	return &Dispatch{MirrorID: id, Headers: out}, nil, nil
}

// Primary 登记主路径响应。错误优先级：参数非法 > 未知 > 重复。
func (m *M) Primary(reqID string, resp diff.Response) error {
	if reqID == "" || !validResponse(resp) {
		return ErrInvalid
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sl, ok := m.reqs[reqID]
	if !ok {
		return ErrUnknown
	}
	if sl.primaryOK || sl.doneErr {
		// 重复登记；错误 Done 之后再到 Primary 亦不允许重复登记且不分类。
		return ErrDuplicate
	}
	sl.primaryOK = true
	sl.primaryResp = resp

	if sl.doneOK {
		m.classify(sl)
	} else {
		sl.primaryFirst = true
		m.pendingDone++
	}
	return nil
}

// Done 登记镜像完成：resp 与 shadowErr 必须恰有一个有效。
func (m *M) Done(mirrorID int64, resp *diff.Response, shadowErr error, now int64) error {
	if mirrorID < 1 {
		return ErrInvalid
	}
	if (resp == nil) == (shadowErr == nil) {
		return ErrInvalid
	}
	if resp != nil && !validResponse(*resp) {
		return ErrInvalid
	}
	if now < 0 || now > maxNowBound {
		return ErrTime
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.smp.CheckClock(now); err != nil {
		if errors.Is(err, sampler.ErrClockBack) {
			return ErrClock
		}
		return ErrTime
	}

	sl, ok := m.ids[mirrorID]
	if !ok {
		return ErrUnknown
	}
	if sl.doneOK || sl.doneErr {
		return ErrDuplicate
	}

	m.inFlight--
	m.completed++
	if shadowErr != nil {
		sl.doneErr = true
		m.shadowErrors++
		m.smp.Fail(now)
		// 错误的镜像永不分类；若 Primary 早已登记，冲销其半到计数，槽位作废。
		if sl.primaryOK {
			m.pendingDone--
		}
		return nil
	}

	sl.doneOK = true
	sl.shadowResp = *resp
	m.smp.Success(now)
	if sl.primaryOK {
		m.classify(sl)
	} else {
		m.pendingPrimary++
	}
	return nil
}

// classify 在持锁且两侧（Primary 与成功 Done）均已到齐时调用，恰好一次。
func (m *M) classify(sl *slot) {
	switch diff.Classify(sl.primaryResp, sl.shadowResp, m.cfg.Ignore) {
	case diff.Identical:
		m.identical++
	case diff.Compatible:
		m.compatible++
	case diff.Breaking:
		m.breaking++
	}
	if sl.primaryFirst {
		m.pendingDone--
	} else {
		m.pendingPrimary--
	}
}

// Stats 返回当前统计快照（读锁，恒满足文档中的恒等式）。
func (m *M) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Stats{
		Accepted:       m.accepted,
		SkippedUnsafe:  m.skipUnsafe,
		SkippedBody:    m.skipBody,
		SkippedPause:   m.skipPause,
		SkippedSample:  m.skipSample,
		SkippedBusy:    m.skipBusy,
		Dispatched:     m.dispatched,
		InFlight:       m.inFlight,
		Completed:      m.completed,
		ShadowErrors:   m.shadowErrors,
		Identical:      m.identical,
		Compatible:     m.compatible,
		Breaking:       m.breaking,
		PendingPrimary: m.pendingPrimary,
		PendingDone:    m.pendingDone,
		PausedUntil:    m.smp.PausedUntil(),
	}
}
