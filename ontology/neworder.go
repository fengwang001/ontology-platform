package ontology

// NewOrder creates an order for account over idents.
//
// For each identifier the account's valid authorization with the largest
// expires is reused (ties broken by smallest id); otherwise a fresh pending
// authorization with expires=now+Ta is created. The order is stored active
// with expires=now+To.
//
// Rejection order: parameter, clock regression, nonce invalid, then per
// identifier rate limiting (t+H > now), then per-account pending quota
// (p+q <= Pm). Rate limiting and quota are evaluated before anything is
// created and before the nonce is consumed.
func (m *Machine) NewOrder(account []byte, idents []string, nonce, now int64) (*Order, error) {
	if len(account) == 0 {
		return nil, errf(KindParam, "account must be non-empty")
	}
	if e := validateIdents(idents); e != nil {
		return nil, e
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if e := m.checkClock(now); e != nil {
		return nil, e
	}
	if _, ok := m.nonceSet[nonce]; !ok {
		return nil, &Error{Kind: KindNonce, Reason: "nonce not in pool", Nonce: nonce}
	}

	// Rate limiting: identifiers are examined in the given order; report the
	// first identifier whose in-window failure count reaches F.
	for _, id := range idents {
		key := reuseKey(account, id)
		cnt := m.failureCount(key, now)
		if cnt >= m.cfg.F {
			return nil, &Error{
				Kind:   KindRateLimited,
				Reason: "failure threshold reached within window",
				Ident:  id,
				Count:  cnt,
			}
		}
	}

	a := m.acc(account)
	p := m.pendingCount(a, now)

	// Determine reuse without mutating structural state yet: probing only
	// unlinks entries already unusable, which is state-independent cleanup.
	type choice struct {
		id    string
		reuse *authz
	}
	choices := make([]choice, len(idents))
	q := 0
	for i, id := range idents {
		key := reuseKey(account, id)
		best := m.probeReuse(key, now)
		choices[i] = choice{id: id, reuse: best}
		if best == nil {
			q++
		}
	}
	if p+q > m.cfg.Pm {
		return nil, &Error{
			Kind:    KindQuota,
			Reason:  "pending authorization quota exceeded",
			Pending: p,
			Need:    q,
		}
	}

	// All checks passed: consume the nonce and create objects.
	m.consumeNonce(nonce)
	m.clock = now

	accCopy := append([]byte(nil), account...)
	azs := make([]*authz, len(idents))
	for i, ch := range choices {
		if ch.reuse != nil {
			azs[i] = ch.reuse
			continue
		}
		m.authzSeq++
		az := &authz{
			id:      m.authzSeq,
			account: accCopy,
			ident:   ch.id,
			status:  stPending,
			expires: now + m.cfg.Ta,
			inIndex: false,
		}
		m.authzs[az.id] = az
		az.inPending = true
		a.pendingList = append(a.pendingList, az)
		azs[i] = az
	}

	m.orderSeq++
	o := &order{
		id:      m.orderSeq,
		account: accCopy,
		idents:  append([]string(nil), idents...),
		authzs:  azs,
		expires: now + m.cfg.To,
		status:  stActive,
	}
	m.orders[o.id] = o

	return m.orderView(o, now), nil
}
