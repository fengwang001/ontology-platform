package ontology

import (
	"strconv"
)

// Report records a CA-internal validation result. It carries no account and
// no nonce. The authorization must be effective-pending. On success it
// becomes valid with expires=now+Tv; otherwise it becomes invalid and one
// failure (account, ident, now) is recorded.
func (m *Machine) Report(authzRef string, ok bool, now int64) error {
	id, parsed := parseRef(authzRef, "z")
	if !parsed {
		return errf(KindParam, "invalid authorization id %q", authzRef)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if e := m.checkClock(now); e != nil {
		return e
	}
	az := m.authzs[id]
	if az == nil {
		return &Error{Kind: KindNotFound, Reason: "authorization not found", Ref: authzRef}
	}
	st := effStatus(az, now)
	if st != stPending {
		return &Error{Kind: KindState, Reason: "authorization not pending", Ref: authzRef, Status: st}
	}

	m.clock = now
	if ok {
		az.status = stValid
		az.expires = now + m.cfg.Tv
		if !az.inIndex {
			key := reuseKey(az.account, az.ident)
			m.reuse[key] = append(m.reuse[key], az)
			az.inIndex = true
		}
	} else {
		az.status = stInvalid
		key := reuseKey(az.account, az.ident)
		m.failures[key] = append(m.failures[key], now)
	}
	return nil
}

// Deactivate sets the authorization to deactivated. It must belong to the
// account and be effective-pending or effective-valid.
func (m *Machine) Deactivate(account []byte, authzRef string, nonce, now int64) error {
	if len(account) == 0 {
		return errf(KindParam, "account must be non-empty")
	}
	id, parsed := parseRef(authzRef, "z")
	if !parsed {
		return errf(KindParam, "invalid authorization id %q", authzRef)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if e := m.checkClock(now); e != nil {
		return e
	}
	if _, ok := m.nonceSet[nonce]; !ok {
		return &Error{Kind: KindNonce, Reason: "nonce not in pool", Nonce: nonce}
	}
	az := m.authzs[id]
	if az == nil || string(az.account) != string(account) {
		return &Error{Kind: KindNotFound, Reason: "authorization not found for account", Ref: authzRef}
	}
	st := effStatus(az, now)
	if st != stPending && st != stValid {
		return &Error{Kind: KindState, Reason: "authorization cannot be deactivated", Ref: authzRef, Status: st}
	}

	m.consumeNonce(nonce)
	m.clock = now
	az.status = stDeactivated
	return nil
}

// Finalize issues a certificate when the order is effective-ready and the
// distinct CSR identifier set equals the order's identifier set. On success
// the order becomes stored-valid and receives the next global certificate
// serial number.
func (m *Machine) Finalize(account []byte, orderRef string, csrIdents []string, nonce, now int64) (int64, error) {
	if len(account) == 0 {
		return 0, errf(KindParam, "account must be non-empty")
	}
	id, parsed := parseRef(orderRef, "o")
	if !parsed {
		return 0, errf(KindParam, "invalid order id %q", orderRef)
	}
	if e := validateCSR(csrIdents); e != nil {
		return 0, e
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if e := m.checkClock(now); e != nil {
		return 0, e
	}
	if _, ok := m.nonceSet[nonce]; !ok {
		return 0, &Error{Kind: KindNonce, Reason: "nonce not in pool", Nonce: nonce}
	}
	o := m.orders[id]
	if o == nil || string(o.account) != string(account) {
		return 0, &Error{Kind: KindNotFound, Reason: "order not found for account", Ref: orderRef}
	}
	st := orderStatus(o, now)
	if st != stReady {
		return 0, &Error{Kind: KindState, Reason: "order not ready", Ref: orderRef, Status: st}
	}
	if !sameSet(csrIdents, o.idents) {
		return 0, &Error{Kind: KindCSR, Reason: "CSR identifier set differs from order", Ref: orderRef}
	}

	m.consumeNonce(nonce)
	m.clock = now
	m.certSeq++
	o.status = stValid
	o.certSN = m.certSeq
	return o.certSN, nil
}

// Status returns the effective order status at now. Read-only: it rejects a
// regressing clock but never advances it and never consumes a nonce.
func (m *Machine) Status(orderRef string, now int64) (string, error) {
	id, parsed := parseRef(orderRef, "o")
	if !parsed {
		return "", errf(KindParam, "invalid order id %q", orderRef)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if e := m.checkClock(now); e != nil {
		return "", e
	}
	o := m.orders[id]
	if o == nil {
		return "", &Error{Kind: KindNotFound, Reason: "order not found", Ref: orderRef}
	}
	return orderStatus(o, now), nil
}

// GetAuthz returns the effective authorization view at now (read-only).
func (m *Machine) GetAuthz(authzRef string, now int64) (*Authz, error) {
	id, parsed := parseRef(authzRef, "z")
	if !parsed {
		return nil, errf(KindParam, "invalid authorization id %q", authzRef)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if e := m.checkClock(now); e != nil {
		return nil, e
	}
	az := m.authzs[id]
	if az == nil {
		return nil, &Error{Kind: KindNotFound, Reason: "authorization not found", Ref: authzRef}
	}
	return m.authzView(az, now), nil
}

// GetOrder returns the effective order view at now (read-only).
func (m *Machine) GetOrder(orderRef string, now int64) (*Order, error) {
	id, parsed := parseRef(orderRef, "o")
	if !parsed {
		return nil, errf(KindParam, "invalid order id %q", orderRef)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if e := m.checkClock(now); e != nil {
		return nil, e
	}
	o := m.orders[id]
	if o == nil {
		return nil, &Error{Kind: KindNotFound, Reason: "order not found", Ref: orderRef}
	}
	return m.orderView(o, now), nil
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}

func (m *Machine) authzView(a *authz, now int64) *Authz {
	return &Authz{
		ID:      "z" + strconv.FormatInt(a.id, 10),
		Account: append([]byte(nil), a.account...),
		Ident:   a.ident,
		Status:  effStatus(a, now),
		Expires: a.expires,
	}
}

func (m *Machine) orderView(o *order, now int64) *Order {
	ids := make([]string, len(o.authzs))
	for i, a := range o.authzs {
		ids[i] = "z" + strconv.FormatInt(a.id, 10)
	}
	return &Order{
		ID:       "o" + strconv.FormatInt(o.id, 10),
		Account:  append([]byte(nil), o.account...),
		Idents:   append([]string(nil), o.idents...),
		AuthzIDs: ids,
		Expires:  o.expires,
		Status:   orderStatus(o, now),
		CertSN:   o.certSN,
	}
}
