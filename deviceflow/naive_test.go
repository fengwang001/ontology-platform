package deviceflow

import (
	"strconv"
)

// naiveService 是按规格逐条写成的朴素模拟：不做任何增量维护，
// 每次统计都全量扫描，用于对照优化实现的行为。
type naiveService struct {
	cfg      Config
	maxNow   int64
	auths    []*naiveAuth
	events   map[string][]int64
	devSeq   int64
	tokSeq   int
	gen      func() string
	genCalls int
}

type naiveAuth struct {
	deviceCode  string
	client      string
	userCode    string
	status      Status
	expiresAt   int64
	interval    int64
	nextAllowed int64
}

func newNaive(cfg Config, gen func() string) *naiveService {
	return &naiveService{cfg: cfg, events: make(map[string][]int64), gen: gen}
}

func (n *naiveService) baseInterval(pen int) int64 {
	if int64(pen) > (n.cfg.Imax-n.cfg.I0)/n.cfg.D {
		return n.cfg.Imax
	}
	return n.cfg.I0 + n.cfg.D*int64(pen)
}

func (n *naiveService) window(client string, now int64) []int64 {
	var w []int64
	for _, t := range n.events[client] {
		if t+n.cfg.H > now {
			w = append(w, t)
		}
	}
	return w
}

func (n *naiveService) Start(client string, now int64) (*StartResult, error) {
	if client == "" {
		return nil, &Error{Kind: KindInvalidParam, Msg: "empty client"}
	}
	if now < 0 || now > maxNowValue {
		return nil, &Error{Kind: KindInvalidParam, Msg: "now out of range"}
	}
	if now < n.maxNow {
		return nil, &Error{Kind: KindClockRollback, Msg: "rollback"}
	}
	w := n.window(client, now)
	pen := len(w)
	if pen >= n.cfg.Z {
		return nil, &Error{Kind: KindRateLimited, Msg: "locked", S: pen, U: w[pen-n.cfg.Z] + n.cfg.H}
	}
	active := 0
	for _, a := range n.auths {
		if a.client == client && (a.status == StatusPending || a.status == StatusApproved) && now < a.expiresAt {
			active++
		}
	}
	if active >= n.cfg.Cmax {
		return nil, &Error{Kind: KindTooManyActive, Msg: "cap"}
	}
	var raw, norm string
	calls := 0
	for {
		raw = n.gen()
		calls++
		nm, ok := normalizeCode(raw)
		dup := false
		if ok {
			for _, a := range n.auths {
				if a.userCode == nm && now < a.expiresAt {
					dup = true
					break
				}
			}
		}
		if ok && !dup {
			norm = nm
			break
		}
		if calls == maxGenAttempts {
			n.genCalls = calls
			return nil, &Error{Kind: KindCodeGeneration, Msg: "gen failed"}
		}
	}
	n.genCalls = calls
	if now > n.maxNow {
		n.maxNow = now
	}
	n.devSeq++
	a := &naiveAuth{
		deviceCode:  "d" + strconv.FormatInt(n.devSeq, 10),
		client:      client,
		userCode:    norm,
		status:      StatusPending,
		expiresAt:   now + n.cfg.E,
		interval:    n.baseInterval(pen),
		nextAllowed: now,
	}
	n.auths = append(n.auths, a)
	return &StartResult{DeviceCode: a.deviceCode, UserCode: raw, Interval: a.interval, ExpiresAt: a.expiresAt}, nil
}

func (n *naiveService) Authorize(userCode string, approve bool, now int64) error {
	norm, ok := normalizeCode(userCode)
	if !ok {
		return &Error{Kind: KindInvalidParam, Msg: "user code not compliant"}
	}
	if now < 0 || now > maxNowValue {
		return &Error{Kind: KindInvalidParam, Msg: "now out of range"}
	}
	if now < n.maxNow {
		return &Error{Kind: KindClockRollback, Msg: "rollback"}
	}
	var latest *naiveAuth
	for _, a := range n.auths {
		if a.userCode == norm {
			latest = a
		}
	}
	if latest == nil {
		return &Error{Kind: KindNotFound, Msg: "not found"}
	}
	if now >= latest.expiresAt {
		return &Error{Kind: KindExpired, Msg: "expired"}
	}
	if latest.status != StatusPending {
		return &Error{Kind: KindAlreadyDecided, Msg: "decided"}
	}
	if now > n.maxNow {
		n.maxNow = now
	}
	if approve {
		latest.status = StatusApproved
	} else {
		latest.status = StatusDenied
	}
	return nil
}

func (n *naiveService) Poll(deviceCode string, now int64) (*PollResult, error) {
	if deviceCode == "" {
		return nil, &Error{Kind: KindInvalidParam, Msg: "empty device code"}
	}
	if now < 0 || now > maxNowValue {
		return nil, &Error{Kind: KindInvalidParam, Msg: "now out of range"}
	}
	if now < n.maxNow {
		return nil, &Error{Kind: KindClockRollback, Msg: "rollback"}
	}
	var a *naiveAuth
	for _, x := range n.auths {
		if x.deviceCode == deviceCode {
			a = x
			break
		}
	}
	if a == nil {
		return nil, &Error{Kind: KindUnknownDeviceCode, Msg: "unknown"}
	}
	if now > n.maxNow {
		n.maxNow = now
	}
	res := &PollResult{}
	switch {
	case a.status == StatusConsumed:
		res.Outcome = OutcomeInvalidGrant
	case now >= a.expiresAt:
		res.Outcome = OutcomeExpired
	case now < a.nextAllowed:
		a.interval += n.cfg.D
		if a.interval > n.cfg.Imax {
			a.interval = n.cfg.Imax
		}
		a.nextAllowed = now + a.interval
		n.events[a.client] = append(n.events[a.client], now)
		res.Outcome = OutcomeSlowDown
	default:
		a.nextAllowed = now + a.interval
		switch a.status {
		case StatusPending:
			res.Outcome = OutcomeAuthorizationPending
		case StatusApproved:
			n.tokSeq++
			res.Outcome = OutcomeToken
			res.Token = "tok-" + strconv.Itoa(n.tokSeq)
			res.TokenSeq = n.tokSeq
			a.status = StatusConsumed
		case StatusDenied:
			res.Outcome = OutcomeAccessDenied
			a.status = StatusConsumed
		}
	}
	res.Interval = a.interval
	res.NextAllowed = a.nextAllowed
	return res, nil
}

func (n *naiveService) Interval(deviceCode string) (*IntervalInfo, error) {
	if deviceCode == "" {
		return nil, &Error{Kind: KindInvalidParam, Msg: "empty device code"}
	}
	for _, a := range n.auths {
		if a.deviceCode == deviceCode {
			return &IntervalInfo{Status: a.status, Interval: a.interval, NextAllowed: a.nextAllowed}, nil
		}
	}
	return nil, &Error{Kind: KindUnknownDeviceCode, Msg: "unknown"}
}

func (n *naiveService) ClientInterval(client string, now int64) (*ClientIntervalInfo, error) {
	if client == "" {
		return nil, &Error{Kind: KindInvalidParam, Msg: "empty client"}
	}
	if now < 0 || now > maxNowValue {
		return nil, &Error{Kind: KindInvalidParam, Msg: "now out of range"}
	}
	if now < n.maxNow {
		return nil, &Error{Kind: KindClockRollback, Msg: "rollback"}
	}
	w := n.window(client, now)
	pen := len(w)
	info := &ClientIntervalInfo{Interval: n.baseInterval(pen), S: pen, Limited: pen >= n.cfg.Z}
	if info.Limited {
		info.U = w[pen-n.cfg.Z] + n.cfg.H
	}
	return info, nil
}
