package ontology

import (
	"container/heap"
	"errors"
	"sort"
	"strings"
)

const (
	KindInvalidArgument = "invalid_argument"
	KindClockRewind     = "clock_rewind"
	KindUnknownDevice   = "unknown_device"
	KindRateLimited     = "rate_limited"
	KindCapacity        = "capacity_exceeded"
	KindGeneration      = "generation_failed"
	KindNotFound        = "not_found"
	KindExpired         = "expired"
	KindAlreadyDecided  = "already_decided"
)

const (
	StatePending  = "pending"
	StateApproved = "approved"
	StateDenied   = "denied"
	StateConsumed = "consumed"
)

const (
	PollTooFast      = "too_fast"
	PollWaiting      = "waiting"
	PollToken        = "token"
	PollAccessDenied = "access_denied"
	PollExpired      = "expired"
	PollInvalid      = "invalid"
)

type Config struct {
	E    int64
	I0   int64
	D    int64
	Imax int64
	H    int64
	Cmax int64
	Z    int64
	Gen  func() string
}

type DetailedError struct {
	Kind               string
	Operation          string
	Client             []byte
	DeviceCode         uint64
	UserCode           string
	NormalizedUserCode string
	S                  int64
	U                  int64
}

func (e *DetailedError) Error() string { return e.Kind }

func (e *DetailedError) Is(target error) bool {
	var other *DetailedError
	return errors.As(target, &other) && other.Kind == e.Kind
}

type StartResult struct {
	DeviceCode uint64
	UserCode   string
	Interval   int64
	ExpiresAt  int64
}

type PollResult struct {
	Kind        string
	Token       uint64
	Interval    int64
	NextAllowed int64
}

type IntervalResult struct {
	State       string
	Interval    int64
	NextAllowed int64
}

type ClientIntervalResult struct {
	S           int64
	Interval    int64
	RateLimited bool
	U           int64
}

type Service struct {
	mu           chan struct{}
	cfg          Config
	gen          func() string
	maxNow       int64
	nextDevice   uint64
	nextToken    uint64
	grants       map[uint64]*grant
	latestByCode map[string]*grant
	clients      map[string]*clientState
	genCalls     int
	expiryPops   int
}

type grant struct {
	id        uint64
	client    []byte
	rawCode   string
	code      string
	state     string
	expiresAt int64
	interval  int64
	next      int64
	active    bool
	heapIndex int
}

type clientState struct {
	events     []int64
	eventStart int
	active     []*grant
	activeSize int64
}

func New(cfg Config) (*Service, error) {
	if !validConfig(cfg) {
		return nil, &DetailedError{Kind: KindInvalidArgument, Operation: "New"}
	}
	return &Service{
		mu:           make(chan struct{}, 1),
		cfg:          cfg,
		gen:          cfg.Gen,
		grants:       make(map[uint64]*grant),
		latestByCode: make(map[string]*grant),
		clients:      make(map[string]*clientState),
	}, nil
}

func NormalizeUserCode(code string) (string, bool) {
	var builder strings.Builder
	for _, r := range code {
		if r == '-' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		if !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return "", false
		}
		builder.WriteRune(r)
	}
	normalized := builder.String()
	if len(normalized) < 4 || len(normalized) > 16 {
		return "", false
	}
	return normalized, true
}

func (s *Service) Start(client []byte, now int64) (StartResult, error) {
	if len(client) == 0 {
		return StartResult{}, &DetailedError{Kind: KindInvalidArgument, Operation: "Start", Client: append([]byte(nil), client...)}
	}
	s.mu <- struct{}{}
	defer func() { <-s.mu }()
	if !validNow(now) {
		return StartResult{}, &DetailedError{Kind: KindInvalidArgument, Operation: "Start", Client: append([]byte(nil), client...)}
	}
	if now < s.maxNow {
		return StartResult{}, &DetailedError{Kind: KindClockRewind, Operation: "Start", Client: append([]byte(nil), client...)}
	}
	key := string(client)
	state := s.clients[key]
	if state == nil {
		state = &clientState{}
		s.clients[key] = state
	}
	sCount, u := eventWindow(state.events[state.eventStart:], s.cfg.H, now, s.cfg.Z)
	if sCount >= s.cfg.Z {
		return StartResult{}, &DetailedError{
			Kind:      KindRateLimited,
			Operation: "Start",
			Client:    append([]byte(nil), client...),
			S:         sCount,
			U:         u,
		}
	}
	if state.activeSize >= s.cfg.Cmax && len(state.active) > 0 && state.active[0].expiresAt > now {
		return StartResult{}, &DetailedError{
			Kind:      KindCapacity,
			Operation: "Start",
			Client:    append([]byte(nil), client...),
			S:         sCount,
		}
	}
	s.genCalls = 0
	var normalized string
	for s.genCalls < 100 {
		rawCode := s.gen()
		s.genCalls++
		var ok bool
		normalized, ok = NormalizeUserCode(rawCode)
		if !ok {
			continue
		}
		if existing := s.latestByCode[normalized]; existing != nil && now < existing.expiresAt {
			continue
		}
		s.maxNow = now
		s.compactEvents(state)
		s.expiryPops = 0
		s.reclaimActive(state, now)
		s.nextDevice++
		record := &grant{
			id:        s.nextDevice,
			client:    append([]byte(nil), client...),
			rawCode:   rawCode,
			code:      normalized,
			state:     StatePending,
			expiresAt: now + s.cfg.E,
			interval:  s.baseInterval(sCount),
			next:      now,
			active:    true,
			heapIndex: -1,
		}
		s.grants[record.id] = record
		s.latestByCode[normalized] = record
		state.activeSize++
		heap.Push(activeHeap{state}, record)
		return StartResult{
			DeviceCode: record.id,
			UserCode:   rawCode,
			Interval:   record.interval,
			ExpiresAt:  record.expiresAt,
		}, nil
	}
	return StartResult{}, &DetailedError{
		Kind:      KindGeneration,
		Operation: "Start",
		Client:    append([]byte(nil), client...),
	}
}

func (s *Service) Authorize(userCode string, approve bool, now int64) error {
	normalized, ok := NormalizeUserCode(userCode)
	if !ok {
		return &DetailedError{
			Kind:               KindInvalidArgument,
			Operation:          "Authorize",
			UserCode:           userCode,
			NormalizedUserCode: normalized,
		}
	}
	s.mu <- struct{}{}
	defer func() { <-s.mu }()
	if !validNow(now) {
		return &DetailedError{
			Kind:               KindInvalidArgument,
			Operation:          "Authorize",
			UserCode:           userCode,
			NormalizedUserCode: normalized,
		}
	}
	if now < s.maxNow {
		return &DetailedError{
			Kind:               KindClockRewind,
			Operation:          "Authorize",
			UserCode:           userCode,
			NormalizedUserCode: normalized,
		}
	}
	record := s.latestByCode[normalized]
	if record == nil || record.code != normalized {
		return &DetailedError{
			Kind:               KindNotFound,
			Operation:          "Authorize",
			UserCode:           userCode,
			NormalizedUserCode: normalized,
		}
	}
	if now >= record.expiresAt {
		return &DetailedError{
			Kind:               KindExpired,
			Operation:          "Authorize",
			DeviceCode:         record.id,
			UserCode:           userCode,
			NormalizedUserCode: normalized,
		}
	}
	if record.state != StatePending {
		return &DetailedError{
			Kind:               KindAlreadyDecided,
			Operation:          "Authorize",
			DeviceCode:         record.id,
			UserCode:           userCode,
			NormalizedUserCode: normalized,
		}
	}
	s.maxNow = now
	if approve {
		record.state = StateApproved
	} else {
		record.state = StateDenied
		state := s.clients[string(record.client)]
		if state != nil {
			removeActive(state, record)
		}
	}
	return nil
}

func (s *Service) Poll(deviceCode uint64, now int64) (PollResult, error) {
	if deviceCode == 0 || !validNow(now) {
		return PollResult{}, &DetailedError{Kind: KindInvalidArgument, Operation: "Poll", DeviceCode: deviceCode}
	}
	s.mu <- struct{}{}
	defer func() { <-s.mu }()
	if now < s.maxNow {
		return PollResult{}, &DetailedError{Kind: KindClockRewind, Operation: "Poll", DeviceCode: deviceCode}
	}
	record := s.grants[deviceCode]
	if record == nil {
		return PollResult{}, &DetailedError{Kind: KindUnknownDevice, Operation: "Poll", DeviceCode: deviceCode}
	}
	s.maxNow = now
	state := s.clients[string(record.client)]
	if record.state == StateConsumed {
		return PollResult{Kind: PollInvalid, Interval: record.interval, NextAllowed: record.next}, nil
	}
	if now >= record.expiresAt {
		if state != nil {
			removeActive(state, record)
		}
		return PollResult{Kind: PollExpired, Interval: record.interval, NextAllowed: record.next}, nil
	}
	if now < record.next {
		interval := record.interval + s.cfg.D
		if interval > s.cfg.Imax {
			interval = s.cfg.Imax
		}
		record.interval = interval
		record.next = now + interval
		if state != nil {
			advanceEvents(state, s.cfg.H, now)
			s.compactEvents(state)
			state.events = append(state.events, now)
		}
		return PollResult{
			Kind:        PollTooFast,
			Interval:    interval,
			NextAllowed: record.next,
		}, nil
	}
	record.next = now + record.interval
	switch record.state {
	case StatePending:
		return PollResult{
			Kind:        PollWaiting,
			Interval:    record.interval,
			NextAllowed: record.next,
		}, nil
	case StateApproved:
		s.nextToken++
		token := s.nextToken
		record.state = StateConsumed
		if state != nil {
			removeActive(state, record)
		}
		return PollResult{
			Kind:        PollToken,
			Token:       token,
			Interval:    record.interval,
			NextAllowed: record.next,
		}, nil
	default:
		record.state = StateConsumed
		if state != nil {
			removeActive(state, record)
		}
		return PollResult{
			Kind:        PollAccessDenied,
			Interval:    record.interval,
			NextAllowed: record.next,
		}, nil
	}
}

func (s *Service) Interval(deviceCode uint64) (IntervalResult, error) {
	if deviceCode == 0 {
		return IntervalResult{}, &DetailedError{Kind: KindInvalidArgument, Operation: "Interval", DeviceCode: deviceCode}
	}
	s.mu <- struct{}{}
	defer func() { <-s.mu }()
	record := s.grants[deviceCode]
	if record == nil {
		return IntervalResult{}, &DetailedError{Kind: KindUnknownDevice, Operation: "Interval", DeviceCode: deviceCode}
	}
	return IntervalResult{
		State:       record.state,
		Interval:    record.interval,
		NextAllowed: record.next,
	}, nil
}

func (s *Service) ClientInterval(client []byte, now int64) (ClientIntervalResult, error) {
	if len(client) == 0 || !validNow(now) {
		return ClientIntervalResult{}, &DetailedError{Kind: KindInvalidArgument, Operation: "ClientInterval", Client: append([]byte(nil), client...)}
	}
	s.mu <- struct{}{}
	defer func() { <-s.mu }()
	if now < s.maxNow {
		return ClientIntervalResult{}, &DetailedError{Kind: KindClockRewind, Operation: "ClientInterval", Client: append([]byte(nil), client...)}
	}
	state := s.clients[string(client)]
	events := []int64(nil)
	if state != nil {
		events = state.events[state.eventStart:]
	}
	first := sort.Search(len(events), func(index int) bool {
		return events[index]+s.cfg.H > now
	})
	events = events[first:]
	sCount := int64(len(events))
	threshold := sCount - s.cfg.Z + 1
	var u int64
	if threshold > 0 {
		u = events[threshold-1] + s.cfg.H
	}
	return ClientIntervalResult{
		S:           sCount,
		Interval:    s.baseInterval(sCount),
		RateLimited: sCount >= s.cfg.Z,
		U:           u,
	}, nil
}
