package kerberos

import (
	"fmt"
	"math/rand"
	"testing"
)

type simConfig struct {
	L, R, S, P int64
}

type simOperation struct {
	name      string
	subject   string
	ticket    string
	service   string
	start     int64
	till      int64
	renewTill int64
	authTime  int64
	now       int64
}

type naiveTicket struct {
	id        string
	kind      string
	subject   string
	start     int64
	end       int64
	renewTill int64
	invalid   bool
	issued    int64
}

type naiveModel struct {
	cfg     simConfig
	now     int64
	hasNow  bool
	next    int64
	tickets map[string]naiveTicket
	keys    map[string]int64
	replay  map[[2]string]int64
}

type simResult struct {
	ok     bool
	reason string
	ticket *Ticket
	naive  *naiveTicket
	basis  string
}

func TestRandomAgainstNaiveSimulation(t *testing.T) {
	cfg := simConfig{L: 100, R: 1000, S: 5, P: 50}
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			k, err := NewKDC(cfg.L, cfg.R, cfg.S, cfg.P)
			if err != nil {
				t.Fatal(err)
			}
			model := newNaiveModel(cfg)
			var now int64 = 1
			var ids []string
			subjects := []string{"alice", "bob"}
			services := []string{"web", "mail", "db"}
			const steps = 80

			for step := 0; step < steps; step++ {
				now += rng.Int63n(12)
				if now > maxTime-2000 {
					now = 1
					model.hasNow = false
					model.now = 0
				}
				op := simOperation{name: "IssueTGT", subject: subjects[rng.Intn(len(subjects))], now: now}
				switch {
				case len(ids) == 0 || rng.Intn(10) == 0:
					op.name = "IssueTGT"
					op.subject = subjects[rng.Intn(len(subjects))]
					if rng.Intn(10) == 0 {
						op.subject = ""
					}
					op.start = []int64{0, now, now + rng.Int63n(cfg.P+2), now - 1}[rng.Intn(4)]
					op.till = positiveNear(rng, now, 250)
					op.renewTill = rng.Int63n(2 * cfg.R)
				case rng.Intn(10) < 2:
					op.name = "ChangeKey"
				default:
					op.ticket = ids[rng.Intn(len(ids))]
					op.name = []string{"TGS", "Renew", "Validate", "Authenticate"}[rng.Intn(4)]
				}

				switch op.name {
				case "TGS":
					op.service = services[rng.Intn(len(services))]
					if rng.Intn(12) == 0 {
						op.service = "krbtgt"
					}
					if rng.Intn(12) == 0 {
						op.service = ""
					}
					op.till = positiveNear(rng, op.now, 250)
					op.renewTill = rng.Int63n(2 * cfg.R)
				case "Authenticate":
					delta := rng.Int63n(cfg.S + 8)
					if rng.Intn(2) == 0 {
						op.authTime = op.now - delta
					} else {
						op.authTime = op.now + delta
					}
					if op.authTime < 1 {
						op.authTime = 1
					}
				}

				var actual *Ticket
				var actualErr error
				switch op.name {
				case "IssueTGT":
					actual, actualErr = k.IssueTGT([]byte(op.subject), op.start, op.till, op.renewTill, op.now)
				case "TGS":
					actual, actualErr = k.TGS(op.ticket, []byte(op.service), op.till, op.renewTill, op.now)
				case "Renew":
					actual, actualErr = k.Renew(op.ticket, op.now)
				case "Validate":
					actual, actualErr = k.Validate(op.ticket, op.now)
				case "Authenticate":
					actual, actualErr = k.Authenticate(op.ticket, op.authTime, op.now)
				case "ChangeKey":
					actualErr = k.ChangeKey([]byte(op.subject), op.now)
				}

				naiveTicketResult, naiveReason, basis := model.apply(op)
				t.Logf("input step=%d op=%s subject=%q ticket=%s service=%q start=%d till=%d renewTill=%d authTime=%d now=%d",
					step, op.name, op.subject, op.ticket, op.service, op.start, op.till, op.renewTill, op.authTime, op.now)
				if actualErr != nil {
					t.Logf("output actual=%s basis=%s", actualErr, basis)
				} else if actual != nil {
					t.Logf("output actual=%s [%d,%d) renewTill=%d invalid=%v issued=%d basis=%s",
						actual.ID, actual.Start, actual.End, actual.RenewTill, actual.Invalid, actual.Issued, basis)
				} else {
					t.Logf("output actual=success basis=%s", basis)
				}
				if naiveReason != "" {
					t.Logf("output naive=%s basis=%s", naiveReason, basis)
				} else if naiveTicketResult != nil {
					t.Logf("output naive=%s [%d,%d) renewTill=%d invalid=%v issued=%d basis=%s",
						naiveTicketResult.id, naiveTicketResult.start, naiveTicketResult.end,
						naiveTicketResult.renewTill, naiveTicketResult.invalid, naiveTicketResult.issued, basis)
				} else {
					t.Logf("output naive=success basis=%s", basis)
				}

				if (actualErr == nil) != (naiveReason == "") {
					t.Fatalf("success mismatch: actual=%v naive=%s (%s)", actualErr, naiveReason, basis)
				}
				if actualErr != nil {
					if actualErr.(*Error).Reason != naiveReason {
						t.Fatalf("reason mismatch actual=%s naive=%s (%s)", actualErr, naiveReason, basis)
					}
				} else {
					if actual != nil {
						if naiveTicketResult == nil {
							t.Fatal("naive result missing ticket")
						}
						ids = append(ids, actual.ID)
						if actual.ID != naiveTicketResult.id ||
							actual.Kind != naiveTicketResult.kind ||
							string(actual.Subject) != naiveTicketResult.subject ||
							actual.Start != naiveTicketResult.start ||
							actual.End != naiveTicketResult.end ||
							actual.RenewTill != naiveTicketResult.renewTill ||
							actual.Invalid != naiveTicketResult.invalid ||
							actual.Issued != naiveTicketResult.issued {
							t.Fatalf("ticket mismatch actual=%+v naive=%+v", actual, naiveTicketResult)
						}
					}
				}
				if len(k.replay.entries) != len(model.replay) {
					t.Fatalf("replay size actual=%d naive=%d", len(k.replay.entries), len(model.replay))
				}
			}
		})
	}
}

func positiveNear(rng *rand.Rand, center int64, spread int64) int64 {
	value := center + rng.Int63n(spread) + 1
	if value > maxTime {
		return maxTime
	}
	return value
}

func newNaiveModel(cfg simConfig) *naiveModel {
	return &naiveModel{
		cfg:     cfg,
		tickets: make(map[string]naiveTicket),
		keys:    make(map[string]int64),
		replay:  make(map[[2]string]int64),
	}
}

func (m *naiveModel) rollback(now int64) bool {
	return m.hasNow && now < m.now
}

func (m *naiveModel) accept(now int64) {
	if !m.hasNow || now > m.now {
		m.now = now
	}
	m.hasNow = true
	for key, expires := range m.replay {
		if m.now > expires {
			delete(m.replay, key)
		}
	}
}

func (m *naiveModel) id() string {
	m.next++
	return fmt.Sprintf("t%d", m.next)
}

func (m *naiveModel) keyChanged(ticket naiveTicket) bool {
	changed, ok := m.keys[ticket.subject]
	return ok && ticket.issued < changed
}

func (m *naiveModel) usable(ticket naiveTicket, now int64) (string, bool) {
	if now < ticket.start {
		return ErrNotYetValid, false
	}
	if ticket.invalid {
		return ErrInvalidTicket, false
	}
	if now >= ticket.end {
		return ErrExpired, false
	}
	if m.keyChanged(ticket) {
		return ErrKeyChanged, false
	}
	return "", true
}

func (m *naiveModel) apply(op simOperation) (*naiveTicket, string, string) {
	invalid := op.subject == "" || op.now < 0 || op.now > maxTime
	switch op.name {
	case "IssueTGT":
		invalid = invalid || op.start < 0 || op.till < 1 || op.renewTill < 0
	case "TGS":
		invalid = invalid || op.service == "" || op.service == "krbtgt" || op.till < 1 || op.renewTill < 0
	case "Authenticate":
		invalid = invalid || op.authTime < 0
	case "Renew", "Validate":
		invalid = invalid || op.ticket == ""
	}
	if invalid {
		return nil, ErrInvalidParameter, "parameter outside spec"
	}
	if m.rollback(op.now) {
		return nil, ErrClockRollback, "now regressed"
	}

	var ticket *naiveTicket
	if op.name != "IssueTGT" && op.name != "ChangeKey" {
		found, ok := m.tickets[op.ticket]
		if !ok {
			return nil, ErrTicketNotFound, "ticket id absent"
		}
		ticket = &found
	}

	switch op.name {
	case "IssueTGT":
		start := op.now
		if op.start != 0 {
			if op.start < op.now {
				return nil, ErrInvalidParameter, "explicit start before now"
			}
			start = op.start
		}
		if op.till <= start {
			return nil, ErrInvalidInterval, "till must follow start"
		}
		if start > op.now+m.cfg.P {
			return nil, ErrTooFarPostdated, "start exceeds now+P"
		}
		end := minInt64(op.till, start+m.cfg.L)
		rt := int64(0)
		if op.renewTill > 0 {
			candidate := minInt64(op.renewTill, start+m.cfg.R)
			if candidate > end {
				rt = candidate
			}
		}
		created := naiveTicket{
			id: m.id(), kind: KindTGT, subject: op.subject, start: start, end: end,
			renewTill: rt, invalid: start > op.now, issued: op.now,
		}
		m.tickets[created.id] = created
		m.accept(op.now)
		result := created
		return &result, "", "end=min(till,start+L), renewable only if capped rt>end"

	case "TGS":
		if ticket.kind != KindTGT {
			return nil, ErrNotTGT, "referenced ticket is not a TGT"
		}
		if reason, ok := m.usable(*ticket, op.now); !ok {
			return nil, reason, "TGT start/invalid/end/key order"
		}
		if op.till <= op.now {
			return nil, ErrInvalidInterval, "till must follow now"
		}
		end := minInt64(op.till, op.now+m.cfg.L, ticket.end)
		rt := int64(0)
		if op.renewTill > 0 && ticket.renewTill > 0 {
			candidate := minInt64(op.renewTill, op.now+m.cfg.R, ticket.renewTill)
			if candidate > end {
				rt = candidate
			}
		}
		created := naiveTicket{
			id: m.id(), kind: KindService, subject: ticket.subject, start: op.now, end: end,
			renewTill: rt, issued: op.now,
		}
		m.tickets[created.id] = created
		m.accept(op.now)
		result := created
		return &result, "", "service end/rt snapshotted from current TGT"

	case "Renew":
		if reason, ok := m.usable(*ticket, op.now); !ok {
			return nil, reason, "ticket start/invalid/end/key order"
		}
		if ticket.renewTill == 0 {
			return nil, ErrNotRenewable, "renewTill is zero"
		}
		life := ticket.end - ticket.start
		newEnd := minInt64(op.now+life, ticket.renewTill)
		if newEnd <= ticket.end {
			return nil, ErrRenewLimit, "new end does not extend current end"
		}
		ticket.start = op.now
		ticket.end = newEnd
		m.tickets[ticket.id] = *ticket
		m.accept(op.now)
		result := *ticket
		return &result, "", "new end=min(now+current life,renewTill)"

	case "Validate":
		if !ticket.invalid {
			return nil, ErrNotPostdated, "ticket is not postdated"
		}
		if op.now < ticket.start {
			return nil, ErrNotYetValid, "postdated ticket has not begun"
		}
		if op.now >= ticket.end {
			return nil, ErrExpired, "validation time is at or after end"
		}
		if m.keyChanged(*ticket) {
			return nil, ErrKeyChanged, "subject key is newer than issued"
		}
		ticket.invalid = false
		m.tickets[ticket.id] = *ticket
		m.accept(op.now)
		result := *ticket
		return &result, "", "postdated flag cleared inside valid interval"

	case "Authenticate":
		diff := op.now - op.authTime
		if diff < 0 {
			diff = -diff
		}
		if diff > m.cfg.S {
			return nil, ErrClockSkew, "clock skew checked first"
		}
		if op.now < ticket.start {
			return nil, ErrNotYetValid, "authentication before start"
		}
		if ticket.invalid {
			return nil, ErrInvalidTicket, "ticket still invalid"
		}
		if op.now >= ticket.end {
			return nil, ErrExpired, "authentication at or after end"
		}
		if m.keyChanged(*ticket) {
			return nil, ErrKeyChanged, "subject key is newer than issued"
		}
		key := [2]string{ticket.id, fmt.Sprintf("%d", op.authTime)}
		if _, ok := m.replay[key]; ok {
			return nil, ErrReplay, "same ticket and authTime retained"
		}
		m.replay[key] = op.authTime + m.cfg.S
		m.accept(op.now)
		result := *ticket
		return &result, "", fmt.Sprintf("cache retained through expires=%d", op.authTime+m.cfg.S)

	case "ChangeKey":
		m.keys[op.subject] = op.now
		m.accept(op.now)
		return nil, "", "subject key version set to now"
	}
	return nil, ErrInvalidParameter, "unknown operation"
}
