package ontology

import (
	"container/list"
	"strconv"
)

func New(config Config) (*StateMachine, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &StateMachine{
		cfg:           config,
		nonces:        make(map[uint64]struct{}),
		nonceOrder:    list.New(),
		nonceElements: make(map[uint64]*list.Element),
		validIndexes:  make(map[string]map[string]*reuseIndex),
		pendingSet:    make(map[string]map[int]struct{}),
		failures:      make(map[string]map[string]*failureQueue),
	}, nil
}

func (m *StateMachine) Nonce() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.nonceCounter++
	nonce := m.nonceCounter
	if int64(m.nonceOrder.Len()) == m.cfg.NonceCapacity {
		front := m.nonceOrder.Front()
		old := m.nonceOrder.Remove(front).(uint64)
		delete(m.nonces, old)
		delete(m.nonceElements, old)
	}
	m.nonces[nonce] = struct{}{}
	m.nonceElements[nonce] = m.nonceOrder.PushBack(nonce)
	return nonce
}

func (m *StateMachine) NewOrder(account []byte, identifiers []string, nonce uint64, now int64) (*Order, error) {
	acc, err := validateAccount(account)
	if err != nil {
		return nil, err
	}
	if !validIdentifierList(identifiers) || !validNow(now) {
		return nil, errInvalid()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.acceptClock(now); err != nil {
		return nil, err
	}
	if _, ok := m.nonces[nonce]; !ok {
		return nil, categorized(KindBadNonce, "", ErrBadNonce)
	}

	for _, identifier := range identifiers {
		count := m.activeFailureCount(acc, identifier, now)
		if int64(count) >= m.cfg.FailureThreshold {
			return nil, rateLimited(identifier, count)
		}
	}

	selected := make([]int, len(identifiers))
	q := 0
	for i, identifier := range identifiers {
		selected[i] = m.findReusableAuthorization(acc, identifier, now)
		if selected[i] == 0 {
			q++
		}
	}

	p := m.pendingCount(acc, now)
	if int64(p+q) > m.cfg.PendingAuthLimit {
		return nil, quotaExceeded(p, q)
	}

	order := &orderRecord{
		id:          len(m.orders) + 1,
		account:     acc,
		identifiers: cloneStrings(identifiers),
		authIDs:     make([]int, len(identifiers)),
		expires:     now + m.cfg.OrderTTL,
		status:      StatusActive,
	}

	for i, identifier := range identifiers {
		if selected[i] != 0 {
			order.authIDs[i] = selected[i]
			continue
		}
		auth := &authRecord{
			id:         len(m.auths) + 1,
			account:    acc,
			identifier: identifier,
			status:     StatusPending,
			expires:    now + m.cfg.AuthPendingTTL,
		}
		m.auths = append(m.auths, auth)
		m.addPending(acc, auth.id)
		order.authIDs[i] = auth.id
	}

	m.orders = append(m.orders, order)
	m.consumeNonce(nonce)
	return m.copyOrder(order), nil
}

func (m *StateMachine) Report(authorizationID string, ok bool, now int64) (*Authorization, error) {
	id, err := parseAuthID(authorizationID)
	if err != nil || !validNow(now) {
		return nil, errInvalid()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.acceptClock(now); err != nil {
		return nil, err
	}
	if id < 1 || id > len(m.auths) {
		return nil, categorized(KindNotFound, "", ErrNotFound)
	}

	auth := m.auths[id-1]
	current := effectiveStatus(auth, now)
	if current != StatusPending {
		return nil, conflict(current)
	}

	if ok {
		auth.status = StatusValid
		auth.expires = now + m.cfg.AuthValidTTL
		m.removePending(auth.account, auth.id)
		m.insertValid(auth)
	} else {
		auth.status = StatusInvalid
		m.removePending(auth.account, auth.id)
		m.appendFailure(auth.account, auth.identifier, now)
	}
	return m.copyAuthorization(auth), nil
}

func (m *StateMachine) Deactivate(account []byte, authorizationID string, nonce uint64, now int64) (*Authorization, error) {
	acc, err := validateAccount(account)
	if err != nil {
		return nil, err
	}
	id, err := parseAuthID(authorizationID)
	if err != nil || !validNow(now) {
		return nil, errInvalid()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.acceptClock(now); err != nil {
		return nil, err
	}
	if _, ok := m.nonces[nonce]; !ok {
		return nil, categorized(KindBadNonce, "", ErrBadNonce)
	}
	if id < 1 || id > len(m.auths) {
		return nil, categorized(KindNotFound, "", ErrNotFound)
	}

	auth := m.auths[id-1]
	if auth.account != acc {
		return nil, categorized(KindNotFound, "", ErrNotFound)
	}
	current := effectiveStatus(auth, now)
	if current != StatusPending && current != StatusValid {
		return nil, conflict(current)
	}

	auth.status = StatusDeactivated
	m.removePending(auth.account, auth.id)
	m.removeValid(auth)
	m.consumeNonce(nonce)
	return m.copyAuthorization(auth), nil
}

func (m *StateMachine) Finalize(account []byte, orderID string, csrIdentifiers []string, nonce uint64, now int64) (*Order, error) {
	acc, err := validateAccount(account)
	if err != nil {
		return nil, err
	}
	id, err := parseOrderID(orderID)
	if err != nil || !validNow(now) || !validIdentifierList(csrIdentifiers) {
		return nil, errInvalid()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.acceptClock(now); err != nil {
		return nil, err
	}
	if _, ok := m.nonces[nonce]; !ok {
		return nil, categorized(KindBadNonce, "", ErrBadNonce)
	}
	if id < 1 || id > len(m.orders) {
		return nil, categorized(KindNotFound, "", ErrNotFound)
	}

	order := m.orders[id-1]
	if order.account != acc {
		return nil, categorized(KindNotFound, "", ErrNotFound)
	}
	current := m.deriveOrderStatus(order, now)
	if current != StatusReady {
		return nil, conflict(current)
	}
	if !sameStringSet(order.identifiers, csrIdentifiers) {
		return nil, categorized(KindCSRMismatch, "", ErrCSRMismatch)
	}

	m.certCount++
	order.status = StatusValid
	order.certSerial = m.certCount
	m.consumeNonce(nonce)
	return m.copyOrder(order), nil
}

func (m *StateMachine) Status(orderID string, now int64) (*StatusResult, error) {
	id, err := parseOrderID(orderID)
	if err != nil || !validNow(now) {
		return nil, errInvalid()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.readClock(now); err != nil {
		return nil, err
	}
	if id < 1 || id > len(m.orders) {
		return nil, categorized(KindNotFound, "", ErrNotFound)
	}

	order := m.orders[id-1]
	return &StatusResult{Order: m.copyOrder(order), Status: m.deriveOrderStatus(order, now)}, nil
}

func (m *StateMachine) Authorization(authorizationID string, now int64) (*Authorization, string, error) {
	id, err := parseAuthID(authorizationID)
	if err != nil || !validNow(now) {
		return nil, "", errInvalid()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.readClock(now); err != nil {
		return nil, "", err
	}
	if id < 1 || id > len(m.auths) {
		return nil, "", categorized(KindNotFound, "", ErrNotFound)
	}

	auth := m.auths[id-1]
	return m.copyAuthorization(auth), effectiveStatus(auth, now), nil
}

func (m *StateMachine) acceptClock(now int64) error {
	if m.clockSet && now < m.clock {
		return categorized(KindClockRollback, strconv.FormatInt(m.clock, 10), ErrClockRollback)
	}
	m.clock = now
	m.clockSet = true
	return nil
}

func (m *StateMachine) readClock(now int64) error {
	if m.clockSet && now < m.clock {
		return categorized(KindClockRollback, strconv.FormatInt(m.clock, 10), ErrClockRollback)
	}
	return nil
}

func (m *StateMachine) consumeNonce(nonce uint64) {
	if element, ok := m.nonceElements[nonce]; ok {
		m.nonceOrder.Remove(element)
	}
	delete(m.nonces, nonce)
	delete(m.nonceElements, nonce)
}
