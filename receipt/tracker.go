package receipt

import (
	"errors"
	"sort"
	"sync"
)

// ReceiptOutcome 是 Receipt 的处置结果。
type ReceiptOutcome int

const (
	// Applied 回执被接受并改变了状态。
	Applied ReceiptOutcome = iota
	// Stale 回执是新的，但按规则不再产生状态推进。
	Stale
	// Duplicate 同一 (kind, attempt) 回执已见过。
	Duplicate
	// Ignored 回执在当前 fail 状态下不允许处置。
	Ignored
)

func (o ReceiptOutcome) String() string {
	switch o {
	case Applied:
		return "Applied"
	case Stale:
		return "Stale"
	case Duplicate:
		return "Duplicate"
	case Ignored:
		return "Ignored"
	default:
		return "Unknown"
	}
}

// Fail 表示收件人的失败标记。
type Fail int

const (
	FailNone Fail = iota
	FailSoft
	FailHard
	FailExpired
)

func (f Fail) String() string {
	switch f {
	case FailNone:
		return "none"
	case FailSoft:
		return "soft"
	case FailHard:
		return "hard"
	case FailExpired:
		return "exp"
	default:
		return "unknown"
	}
}

// RcptStatus 是单个收件人的状态：r 进展值、fail 失败标记、late 迟到硬退信标记。
type RcptStatus struct {
	R    int
	Fail Fail
	Late bool
}

// MessageStatus 是整条消息的汇总。
type MessageStatus struct {
	// Summary 为 failed、inflight、partial、read、delivered 之一。
	Summary string
	// Rcpts 以收件人字节序键给出每个收件人的状态。
	Rcpts map[string]RcptStatus
}

// 拒绝原因（哨兵错误），按题目要求的优先级只报第一个。
var (
	ErrInvalidArgument = errors.New("receipt: invalid argument")
	ErrMessageExists   = errors.New("receipt: message already registered")
	ErrMessageNotFound = errors.New("receipt: message not found")
	ErrUnknownRcpt     = errors.New("receipt: recipient not part of message")
	ErrClockSkew       = errors.New("receipt: tick now must not go backwards")
)

type rcpt struct {
	r       int
	fail    Fail
	sentA   int
	softSet map[int]bool
	seen    map[receiptKey]bool
	late    bool
}

type receiptKey struct {
	kind    string
	attempt int
}

type message struct {
	rcpts    map[string]*rcpt
	order    []string
	deadline int64
}

// Tracker 是并发安全的多收件人消息投递回执状态机。
type Tracker struct {
	mu       sync.Mutex
	softMax  int
	msgs     map[string]*message
	lastTick int64
}

const maxTS = int64(1_000_000_000_000)

var validKind = map[string]int{
	"sent": 1, "delivered": 2, "read": 3, "soft": 0, "hard": 0,
}

// NewTracker 以软退信阈值 S（1..16）构造状态机。
func NewTracker(s int) (*Tracker, error) {
	if s < 1 || s > 16 {
		return nil, ErrInvalidArgument
	}
	return &Tracker{softMax: s, msgs: map[string]*message{}}, nil
}

// Send 登记一条消息：msg 非空且未登记，rcpts 为 1..32 个互异非空收件人，deadline 0..10^12。
func (t *Tracker) Send(msg []byte, rcpts [][]byte, deadline int64) error {
	if len(msg) == 0 || len(rcpts) < 1 || len(rcpts) > 32 || deadline < 0 || deadline > maxTS {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, rc := range rcpts {
		name := string(rc)
		if len(rc) == 0 || seen[name] {
			return ErrInvalidArgument
		}
		seen[name] = true
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	key := string(msg)
	if _, ok := t.msgs[key]; ok {
		return ErrMessageExists
	}

	order := make([]string, 0, len(rcpts))
	byName := make(map[string]*rcpt, len(rcpts))
	for _, rc := range rcpts {
		name := string(rc)
		order = append(order, name)
		byName[name] = &rcpt{softSet: map[int]bool{}, seen: map[receiptKey]bool{}}
	}
	sort.Strings(order)
	t.msgs[key] = &message{rcpts: byName, order: order, deadline: deadline}
	return nil
}

// Receipt 处置一条回执，返回 Applied/Stale/Duplicate/Ignored；被拒绝时不改任何状态。
func (t *Tracker) Receipt(msg []byte, rcptName []byte, kind string, attempt int, ts int64) (ReceiptOutcome, error) {
	progress, kindOK := validKind[kind]
	if !kindOK || attempt < 1 || attempt > 16 || ts < 0 || ts > maxTS {
		return 0, ErrInvalidArgument
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	m, ok := t.msgs[string(msg)]
	if !ok {
		return 0, ErrMessageNotFound
	}
	r, ok := m.rcpts[string(rcptName)]
	if !ok {
		return 0, ErrUnknownRcpt
	}

	key := receiptKey{kind: kind, attempt: attempt}
	// 处置次序第一步：重复检测，命中则什么都不改。
	if r.seen[key] {
		return Duplicate, nil
	}
	r.seen[key] = true

	switch kind {
	case "sent", "delivered", "read":
		return applyProgress(m, r, kind, attempt, progress, ts), nil
	case "soft":
		return t.applySoft(r, attempt), nil
	case "hard":
		return t.applyHard(r), nil
	}
	// 不可达：kind 已在入口校验。
	return Ignored, nil
}

func applyProgress(m *message, r *rcpt, kind string, attempt, progress int, ts int64) ReceiptOutcome {
	switch r.fail {
	case FailSoft, FailHard:
		// 软/硬退信为终态，任何进展回执均无效。
		return Ignored
	case FailExpired:
		// 到期可撤销：仅 delivered/read 且 ts 严格早于 deadline。
		if (kind == "delivered" || kind == "read") && ts < m.deadline {
			r.fail = FailNone
			if progress > r.r {
				r.r = progress
			}
			return Applied
		}
		return Ignored
	}

	// fail 为无。
	if kind == "sent" && attempt > r.sentA {
		r.sentA = attempt
	}
	if progress > r.r {
		r.r = progress
		return Applied
	}
	// 新回执但未推进任何状态（例如 sent 晚于 delivered、read 后到 delivered）。
	return Stale
}

func (t *Tracker) applySoft(r *rcpt, attempt int) ReceiptOutcome {
	if r.fail != FailNone {
		// 已有任何失败标记（含 exp）：软退信不可处置、不可撤销。
		return Ignored
	}
	if r.r >= 2 {
		// 已送达/已读后的软退信是陈旧回执。
		return Stale
	}
	r.softSet[attempt] = true
	// cnt：不小于最近一次 sent 尝试号的有效软退信数；更早的软退信已作废。
	cnt := 0
	for a := range r.softSet {
		if a >= r.sentA {
			cnt++
		}
	}
	if cnt >= t.softMax {
		// 达到阈值后软退信为终态，此后不可撤销。
		r.fail = FailSoft
	}
	return Applied
}

func (t *Tracker) applyHard(r *rcpt) ReceiptOutcome {
	if r.fail == FailSoft || r.fail == FailHard {
		return Ignored
	}
	if r.r >= 2 {
		// 已送达后的硬退信迟到：只做标记，不改 r/fail。
		r.late = true
		return Stale
	}
	// 含由 exp 升级为 hard。
	r.fail = FailHard
	return Applied
}

// Tick 推进时钟：deadline <= now 且 fail 为无、r < 2 的收件人置 exp。
// now 必须单调不减；返回本次新置 exp 的 (msg, rcpt) 名单，按字节序升序。
func (t *Tracker) Tick(now int64) ([][2]string, error) {
	if now < 0 || now > maxTS {
		return nil, ErrInvalidArgument
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if now < t.lastTick {
		return nil, ErrClockSkew
	}
	t.lastTick = now

	var expired [][2]string
	for msgKey, m := range t.msgs {
		if m.deadline > now {
			continue
		}
		for _, name := range m.order {
			r := m.rcpts[name]
			if r.fail == FailNone && r.r < 2 {
				r.fail = FailExpired
				expired = append(expired, [2]string{msgKey, name})
			}
		}
	}
	// 消息映射遍历顺序随机，统一按 (msg, rcpt) 字节序排序。
	sort.Slice(expired, func(i, j int) bool {
		if expired[i][0] != expired[j][0] {
			return expired[i][0] < expired[j][0]
		}
		return expired[i][1] < expired[j][1]
	})
	return expired, nil
}

// Status 返回消息汇总及各收件人 (r, fail, late) 明细。
func (t *Tracker) Status(msg []byte) (MessageStatus, error) {
	if len(msg) == 0 {
		return MessageStatus{}, ErrInvalidArgument
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	m, ok := t.msgs[string(msg)]
	if !ok {
		return MessageStatus{}, ErrMessageNotFound
	}

	n := len(m.order)
	failed, pending, allRead := 0, 0, true
	details := make(map[string]RcptStatus, n)
	for _, name := range m.order {
		r := m.rcpts[name]
		details[name] = RcptStatus{R: r.r, Fail: r.fail, Late: r.late}
		if r.fail != FailNone {
			failed++
		}
		if r.fail == FailNone && r.r < 2 {
			pending++
		}
		if r.r != 3 {
			allRead = false
		}
	}

	summary := ""
	switch {
	case failed == n:
		summary = "failed"
	case pending > 0:
		summary = "inflight"
	case failed > 0:
		// 全部已决（无 pending）但存在失败：部分失败。
		summary = "partial"
	case allRead:
		summary = "read"
	default:
		summary = "delivered"
	}
	return MessageStatus{Summary: summary, Rcpts: details}, nil
}
