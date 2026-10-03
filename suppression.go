package ontology

import "sync"

type Reason uint8

const (
	None Reason = iota
	Soft
	Unsub
	Hard
	Complaint
	Domain
)

const (
	ErrInvalidParameter = ValidationError("invalid parameter")
	ErrInvalidAddress   = ValidationError("invalid address")
	ErrClockRollback    = ClockError("clock rollback")
)

const (
	ErrRecoveryForbidden      = RecoveryError("recovery forbidden")
	ErrNoRecoveryNeeded       = RecoveryError("no recovery needed")
	ErrNoConfirmationToken    = ConfirmationError("no confirmation token")
	ErrStaleToken             = ConfirmationError("stale confirmation token")
	ErrTokenExpired           = ConfirmationError("confirmation token expired")
	ErrComplaintActive        = ConfirmationError("complaint cannot be confirmed")
	ErrNotRecoverable         = ConfirmationError("address is not hard bounced or unsubscribed")
	ErrTokenBeforeSuppression = ConfirmationError("confirmation token predates suppression")
)

type ValidationError string
type ClockError string
type RecoveryError string
type ConfirmationError string

func (e ValidationError) Error() string   { return string(e) }
func (e ClockError) Error() string        { return string(e) }
func (e RecoveryError) Error() string     { return string(e) }
func (e ConfirmationError) Error() string { return string(e) }

func (r Reason) String() string {
	switch r {
	case Soft:
		return "soft"
	case Unsub:
		return "unsub"
	case Hard:
		return "hard"
	case Complaint:
		return "complaint"
	case Domain:
		return "domain"
	default:
		return "none"
	}
}

type addressState struct {
	domain        string
	reason        Reason
	since         int64
	softUntil     int64
	log           []int64
	lastHard      int64
	tokenSeq      int64
	tokenIssuedAt int64
	hasToken      bool
	confirmedAt   int64
}

type domainState struct {
	since     int64
	until     int64
	addresses map[string]*addressState
	activated bool
}

type managerConfig struct {
	softThreshold       int64
	softWindow          int64
	softTTL             int64
	tokenTTL            int64
	domainHardThreshold int64
	domainWindow        int64
	domainTTL           int64
}

type Manager struct {
	mu sync.Mutex

	config managerConfig

	addresses map[string]*addressState
	domains   map[string]*domainState

	maxNow  int64
	nextSeq int64

	domainScanCount int
}

func NewManager(softThreshold, softWindow, softTTL, tokenTTL, domainHardThreshold, domainWindow, domainTTL int64) (*Manager, error) {
	if softThreshold < 1 || softThreshold > 16 ||
		softWindow < 1 || softWindow > 1_000_000_000 ||
		softTTL < 1 || softTTL > 1_000_000_000 ||
		tokenTTL < 1 || tokenTTL > 1_000_000_000 ||
		domainHardThreshold < 1 || domainHardThreshold > 1000 ||
		domainWindow < 1 || domainWindow > 1_000_000_000 ||
		domainTTL < 1 || domainTTL > 1_000_000_000 {
		return nil, ErrInvalidParameter
	}

	return &Manager{
		config: managerConfig{
			softThreshold:       softThreshold,
			softWindow:          softWindow,
			softTTL:             softTTL,
			tokenTTL:            tokenTTL,
			domainHardThreshold: domainHardThreshold,
			domainWindow:        domainWindow,
			domainTTL:           domainTTL,
		},
		addresses: make(map[string]*addressState),
		domains:   make(map[string]*domainState),
		nextSeq:   1,
	}, nil
}

func (m *Manager) Soft(address string, now int64) error {
	normalized, keyDomain, _, err := m.begin(address, now)
	if err != nil {
		return err
	}
	defer m.mu.Unlock()

	state := m.stateFor(normalized, keyDomain)
	m.lazyRecover(state, now)
	if state.reason != None {
		return nil
	}

	cutoff := now - m.config.softWindow
	kept := make([]int64, 0, len(state.log))
	for _, bouncedAt := range state.log {
		if bouncedAt > cutoff {
			kept = append(kept, bouncedAt)
		}
	}
	kept = append(kept, now)
	if int64(len(kept)) < m.config.softThreshold {
		state.log = kept
		return nil
	}

	state.reason = Soft
	state.since = now
	state.softUntil = now + m.config.softTTL
	state.log = nil
	return nil
}

func (m *Manager) Hard(address string, now int64) error {
	normalized, domain, _, err := m.begin(address, now)
	if err != nil {
		return err
	}
	defer m.mu.Unlock()

	state := m.stateFor(normalized, domain)
	m.lazyRecover(state, now)
	state.lastHard = now
	if state.reason < Hard {
		state.reason = Hard
		state.since = now
		state.log = nil
	}

	cutoff := now - m.config.domainWindow
	m.domainScanCount = 0
	hardAddresses := 0
	domainState := m.domains[domain]
	for _, candidate := range domainState.addresses {
		m.domainScanCount++
		if candidate.lastHard > cutoff {
			hardAddresses++
		}
	}

	if int64(hardAddresses) >= m.config.domainHardThreshold {
		wasActive := domainState.until > now
		if now+m.config.domainTTL > domainState.until {
			domainState.until = now + m.config.domainTTL
		}
		if !wasActive {
			domainState.since = now
		}
		domainState.activated = true
	}
	return nil
}

func (m *Manager) Complaint(address string, now int64) error {
	normalized, keyDomain, _, err := m.begin(address, now)
	if err != nil {
		return err
	}
	defer m.mu.Unlock()

	state := m.stateFor(normalized, keyDomain)
	m.lazyRecover(state, now)
	if state.reason < Complaint {
		state.reason = Complaint
		state.since = now
	}
	return nil
}

func (m *Manager) Unsub(address string, now int64) error {
	normalized, keyDomain, _, err := m.begin(address, now)
	if err != nil {
		return err
	}
	defer m.mu.Unlock()

	state := m.stateFor(normalized, keyDomain)
	m.lazyRecover(state, now)
	if state.reason == None || state.reason == Soft {
		state.reason = Unsub
		state.since = now
	}
	return nil
}

func (m *Manager) RequestConfirm(address string, now int64) (int64, error) {
	normalized, _, _, err := m.begin(address, now)
	if err != nil {
		return 0, err
	}
	defer m.mu.Unlock()

	state := m.getState(normalized)
	if state.reason == Complaint {
		return 0, ErrRecoveryForbidden
	}
	if state.reason != Hard && state.reason != Unsub {
		return 0, ErrNoRecoveryNeeded
	}

	seq := m.nextSeq
	m.nextSeq++
	state.hasToken = true
	state.tokenSeq = seq
	state.tokenIssuedAt = now
	return seq, nil
}

func (m *Manager) Confirm(address string, seq, now int64) error {
	normalized, _, _, err := m.begin(address, now)
	if err != nil {
		return err
	}
	defer m.mu.Unlock()

	state := m.getState(normalized)
	if !state.hasToken {
		return ErrNoConfirmationToken
	}
	if seq != state.tokenSeq {
		return ErrStaleToken
	}
	if now >= state.tokenIssuedAt+m.config.tokenTTL {
		return ErrTokenExpired
	}
	if state.reason == Complaint {
		return ErrComplaintActive
	}
	if state.reason != Hard && state.reason != Unsub {
		return ErrNotRecoverable
	}
	if state.tokenIssuedAt <= state.since {
		return ErrTokenBeforeSuppression
	}

	state.reason = None
	state.log = nil
	state.hasToken = false
	state.tokenSeq = 0
	state.tokenIssuedAt = 0
	state.confirmedAt = now
	return nil
}

func (m *Manager) IsSuppressed(address string, now int64) (bool, Reason, error) {
	normalized, domain, _, err := m.begin(address, now)
	if err != nil {
		return false, None, err
	}
	defer m.mu.Unlock()

	state := m.getState(normalized)
	m.lazyRecover(state, now)
	if state.reason != None {
		return true, state.reason, nil
	}

	domainState := m.getDomain(domain)
	if domainState != nil && domainState.activated && domainState.until > now && state.confirmedAt < domainState.since {
		return true, Domain, nil
	}
	return false, None, nil
}

func (m *Manager) begin(address string, now int64) (string, string, string, error) {
	normalized, keyDomain, domain, ok := normalizeAddress(address)
	if !ok {
		return "", "", "", ErrInvalidAddress
	}
	m.mu.Lock()
	if now < 0 || now > 1_000_000_000_000 || now < m.maxNow {
		m.mu.Unlock()
		return "", "", "", ErrClockRollback
	}
	m.maxNow = now
	return normalized, keyDomain, domain, nil
}

func (m *Manager) stateFor(normalized, domain string) *addressState {
	state, ok := m.addresses[normalized]
	if !ok {
		state = &addressState{
			domain:      domain,
			lastHard:    -1,
			confirmedAt: -1,
		}
		m.addresses[normalized] = state
		dom, ok := m.domains[domain]
		if !ok {
			dom = &domainState{addresses: make(map[string]*addressState)}
			m.domains[domain] = dom
		}
		dom.addresses[normalized] = state
	}
	return state
}

func (m *Manager) getState(normalized string) *addressState {
	if state, ok := m.addresses[normalized]; ok {
		return state
	}
	return &addressState{lastHard: -1, confirmedAt: -1}
}

func (m *Manager) getDomain(domain string) *domainState {
	return m.domains[domain]
}

func (m *Manager) lastDomainScanCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.domainScanCount
}

func (m *Manager) lazyRecover(state *addressState, now int64) {
	if state.reason == Soft && now >= state.softUntil {
		state.reason = None
		state.log = nil
	}
}
