package ontology

import (
	"errors"
	"math/rand"
	"testing"
)

type naiveAddress struct {
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

type naiveDomain struct {
	since     int64
	until     int64
	activated bool
	addresses map[string]struct{}
}

type naiveModel struct {
	s        int64
	w        int64
	softTTL  int64
	tokenTTL int64
	kd       int64
	wd       int64
	domTTL   int64

	addresses map[string]*naiveAddress
	domains   map[string]*naiveDomain
	maxNow    int64
	nextSeq   int64
}

type fuzzOperation struct {
	kind string
	addr string
	now  int64
	seq  int64
}

type fuzzResult struct {
	err        error
	seq        int64
	suppressed bool
	reason     Reason
}

func newNaiveModel(s, w, softTTL, tokenTTL, kd, wd, domTTL int64) *naiveModel {
	return &naiveModel{
		s:         s,
		w:         w,
		softTTL:   softTTL,
		tokenTTL:  tokenTTL,
		kd:        kd,
		wd:        wd,
		domTTL:    domTTL,
		addresses: make(map[string]*naiveAddress),
		domains:   make(map[string]*naiveDomain),
		nextSeq:   1,
	}
}

func (m *naiveModel) state(normalized, domain string) *naiveAddress {
	state, ok := m.addresses[normalized]
	if !ok {
		state = &naiveAddress{domain: domain, lastHard: -1, confirmedAt: -1}
		m.addresses[normalized] = state
	}
	dom, exists := m.domains[domain]
	if !exists {
		dom = &naiveDomain{addresses: make(map[string]struct{})}
		m.domains[domain] = dom
	}
	if dom.addresses == nil {
		dom.addresses = make(map[string]struct{})
	}
	dom.addresses[normalized] = struct{}{}
	return state
}

func (m *naiveModel) lazy(state *naiveAddress, now int64) {
	if state.reason == Soft && now >= state.softUntil {
		state.reason = None
		state.log = nil
	}
}

func (m *naiveModel) apply(op fuzzOperation) fuzzResult {
	normalized, domain, _, ok := normalizeAddress(op.addr)
	if !ok {
		return fuzzResult{err: ErrInvalidAddress}
	}
	if op.now < 0 || op.now > 1_000_000_000_000 || op.now < m.maxNow {
		return fuzzResult{err: ErrClockRollback}
	}
	m.maxNow = op.now
	var state *naiveAddress
	switch op.kind {
	case "request", "confirm", "is":
		state = m.getState(normalized)
	default:
		state = m.state(normalized, domain)
	}

	switch op.kind {
	case "soft":
		m.lazy(state, op.now)
		if state.reason != None {
			return fuzzResult{}
		}
		cutoff := op.now - m.w
		kept := make([]int64, 0, len(state.log))
		for _, value := range state.log {
			if value > cutoff {
				kept = append(kept, value)
			}
		}
		kept = append(kept, op.now)
		if int64(len(kept)) >= m.s {
			state.reason = Soft
			state.since = op.now
			state.softUntil = op.now + m.softTTL
			state.log = nil
		} else {
			state.log = kept
		}
	case "hard":
		m.lazy(state, op.now)
		state.lastHard = op.now
		if state.reason < Hard {
			state.reason = Hard
			state.since = op.now
			state.log = nil
		}
		count := 0
		dom := m.domains[domain]
		for address := range dom.addresses {
			candidate := m.addresses[address]
			if candidate.lastHard > op.now-m.wd {
				count++
			}
		}
		if int64(count) >= m.kd {
			activeBefore := dom.until > op.now
			if op.now+m.domTTL > dom.until {
				dom.until = op.now + m.domTTL
			}
			if !activeBefore {
				dom.since = op.now
			}
			dom.activated = true
		}
	case "complaint":
		m.lazy(state, op.now)
		if state.reason < Complaint {
			state.reason = Complaint
			state.since = op.now
		}
	case "unsub":
		m.lazy(state, op.now)
		if state.reason == None || state.reason == Soft {
			state.reason = Unsub
			state.since = op.now
		}
	case "request":
		if state.reason == Complaint {
			return fuzzResult{err: ErrRecoveryForbidden}
		}
		if state.reason != Hard && state.reason != Unsub {
			return fuzzResult{err: ErrNoRecoveryNeeded}
		}
		state.hasToken = true
		state.tokenSeq = m.nextSeq
		state.tokenIssuedAt = op.now
		m.nextSeq++
		return fuzzResult{seq: state.tokenSeq}
	case "confirm":
		if !state.hasToken {
			return fuzzResult{err: ErrNoConfirmationToken}
		}
		if op.seq != state.tokenSeq {
			return fuzzResult{err: ErrStaleToken}
		}
		if op.now >= state.tokenIssuedAt+m.tokenTTL {
			return fuzzResult{err: ErrTokenExpired}
		}
		if state.reason == Complaint {
			return fuzzResult{err: ErrComplaintActive}
		}
		if state.reason != Hard && state.reason != Unsub {
			return fuzzResult{err: ErrNotRecoverable}
		}
		if state.tokenIssuedAt <= state.since {
			return fuzzResult{err: ErrTokenBeforeSuppression}
		}
		state.reason = None
		state.log = nil
		state.hasToken = false
		state.tokenSeq = 0
		state.tokenIssuedAt = 0
		state.confirmedAt = op.now
	case "is":
		m.lazy(state, op.now)
		if state.reason != None {
			return fuzzResult{suppressed: true, reason: state.reason}
		}
		dom := m.domains[domain]
		if dom != nil && dom.activated && dom.until > op.now && state.confirmedAt < dom.since {
			return fuzzResult{suppressed: true, reason: Domain}
		}
	}
	return fuzzResult{}
}

func (m *naiveModel) getState(normalized string) *naiveAddress {
	if state, ok := m.addresses[normalized]; ok {
		return state
	}
	return &naiveAddress{lastHard: -1, confirmedAt: -1}
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	const groups = 2000
	kinds := []string{"soft", "hard", "complaint", "unsub", "request", "confirm", "is"}

	for group := int64(1); group <= groups; group++ {
		rng := rand.New(rand.NewSource(group))
		s := int64(1 + rng.Intn(4))
		w := int64(1 + rng.Intn(12))
		softTTL := int64(1 + rng.Intn(15))
		tokenTTL := int64(1 + rng.Intn(10))
		kd := int64(1 + rng.Intn(3))
		wd := int64(1 + rng.Intn(15))
		domTTL := int64(1 + rng.Intn(20))

		manager, err := NewManager(s, w, softTTL, tokenTTL, kd, wd, domTTL)
		if err != nil {
			t.Fatalf("group %d NewManager: %v", group, err)
		}
		model := newNaiveModel(s, w, softTTL, tokenTTL, kd, wd, domTTL)

		var now int64
		lastSeq := int64(0)
		operations := 1 + rng.Intn(80)
		for i := 0; i < operations; i++ {
			if rng.Intn(5) == 0 {
				now += int64(rng.Intn(8))
			}
			if rng.Intn(20) == 0 {
				now--
			}

			domain := []string{"gmail.com", "googlemail.com", "corp.com", "x.io"}[rng.Intn(4)]
			local := []string{"a", "a.b", "ab+x", "+bad", "x.y+z"}[rng.Intn(5)]
			address := local + "@" + domain
			if rng.Intn(10) == 0 {
				address = []string{"bad", "a@b", "a@x", "a@x..io", " a@x.io"}[rng.Intn(5)]
			}
			op := fuzzOperation{kind: kinds[rng.Intn(len(kinds))], addr: address, now: now}
			if op.kind == "confirm" {
				if rng.Intn(2) == 0 {
					op.seq = lastSeq
				} else {
					op.seq = lastSeq + int64(rng.Intn(3)) - 1
				}
			}

			want := model.apply(op)
			var got fuzzResult
			switch op.kind {
			case "soft":
				got.err = manager.Soft(op.addr, op.now)
			case "hard":
				got.err = manager.Hard(op.addr, op.now)
			case "complaint":
				got.err = manager.Complaint(op.addr, op.now)
			case "unsub":
				got.err = manager.Unsub(op.addr, op.now)
			case "request":
				got.seq, got.err = manager.RequestConfirm(op.addr, op.now)
			case "confirm":
				got.err = manager.Confirm(op.addr, op.seq, op.now)
			case "is":
				got.suppressed, got.reason, got.err = manager.IsSuppressed(op.addr, op.now)
			}

			basis := describeBasis(op, got)
			if testing.Verbose() {
				t.Logf("group=%d seed=%d op=%d input=%s addr=%q now=%d seq=%d output=err=%v seq=%d suppressed=%v reason=%s naive=(err=%v seq=%d suppressed=%v reason=%s) basis=%s",
					group, group, i, op.kind, op.addr, op.now, op.seq,
					got.err, got.seq, got.suppressed, got.reason,
					want.err, want.seq, want.suppressed, want.reason, basis)
			}

			if !errors.Is(got.err, want.err) || got.seq != want.seq || got.suppressed != want.suppressed || got.reason != want.reason {
				t.Logf("group=%d seed=%d op=%d input=%s addr=%q now=%d seq=%d output=err=%v seq=%d suppressed=%v reason=%s naive=(err=%v seq=%d suppressed=%v reason=%s) basis=%s",
					group, group, i, op.kind, op.addr, op.now, op.seq,
					got.err, got.seq, got.suppressed, got.reason,
					want.err, want.seq, want.suppressed, want.reason, basis)
				t.Fatalf("group %d op %d %+v: got %+v, want %+v", group, i, op, got, want)
			}
			if op.kind == "request" && got.err == nil {
				lastSeq = got.seq
			}
		}
	}
}

func describeBasis(op fuzzOperation, result fuzzResult) string {
	if result.err != nil {
		return "first failed validation or ordered recovery check"
	}
	switch op.kind {
	case "soft":
		return "retain timestamps strictly inside window and raise when count reaches S"
	case "hard":
		return "refresh lastHard, never lower reason, and aggregate same-domain recent hard bounces"
	case "complaint":
		return "raise to complaint only when current reason is lower"
	case "unsub":
		return "replace only none or expired/active soft"
	case "request":
		return "hard or unsub may request; complaint is forbidden"
	case "confirm":
		return "six ordered checks pass, then clear suppression and token"
	default:
		return "address reason is checked first, then active domain and confirmation exemption"
	}
}
