package kerberos

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

const diffOpKinds = 6

const (
	kindIssue = iota
	kindTGS
	kindRenew
	kindValidate
	kindAuth
	kindChange
)

type genOp struct {
	kind                             int
	subject, service                 string
	id                               int64
	start, till, renewTill, authTime int64
	now                              int64
}

func genSeq(rng *rand.Rand, n int, L, R, S, P int64) []genOp {
	subjects := []string{"alice", "bob", "carol", "dave"}
	services := []string{"web", "db", "mail", "fs"}
	ops := make([]genOp, 0, n)
	now := int64(0)
	for i := 0; i < n; i++ {
		switch rng.Intn(12) {
		case 0, 1, 2, 3:
			now += int64(rng.Intn(12))
		case 10: // stand still
		case 11: // rejected rewind
			if now > 3 {
				now -= 1 + int64(rng.Intn(3))
			}
		default:
			now += int64(rng.Intn(int(P) + 5))
		}
		if now > maxTime {
			now = maxTime
		}
		o := genOp{kind: rng.Intn(diffOpKinds), now: now}
		o.subject = subjects[rng.Intn(len(subjects))]
		switch o.kind {
		case kindIssue:
			switch rng.Intn(10) {
			case 0:
				o.start = 0
			case 1:
				o.start = now + P + 1 + int64(rng.Intn(4))
			case 2:
				if now > 5 {
					o.start = now - 1 - int64(rng.Intn(3))
				} else {
					o.start = now + int64(rng.Intn(int(P)+1))
				}
			default:
				o.start = now + int64(rng.Intn(int(P)+1))
			}
			if o.start < 0 {
				o.start = 0
			}
			o.till = now + 1 + int64(rng.Intn(int(L)+int(R)+60))
			if rng.Intn(2) == 0 {
				o.renewTill = now + int64(rng.Intn(int(R)+30))
			}
		case kindTGS:
			o.id = int64(1 + rng.Intn(2*i+6))
			o.service = services[rng.Intn(len(services))]
			o.till = now + 1 + int64(rng.Intn(int(L)+int(R)+60))
			if rng.Intn(2) == 0 {
				o.renewTill = now + int64(rng.Intn(int(R)+30))
			}
		case kindRenew, kindValidate:
			o.id = int64(1 + rng.Intn(2*i+6))
		case kindAuth:
			o.id = int64(1 + rng.Intn(2*i+6))
			switch rng.Intn(10) {
			case 0:
				o.authTime = now - S // exact skew bound
			case 1:
				o.authTime = now - S - 1 // skew reject
			case 2:
				o.authTime = now - S - 20
			case 3:
				o.authTime = now + S + 1
			case 4:
				o.authTime = now + S // future skew bound
			default:
				o.authTime = now - int64(rng.Intn(int(S)+1))
			}
			if o.authTime < 0 {
				o.authTime = now
			}
		}
		ops = append(ops, o)
	}
	return ops
}

func runNaive(t *testing.T, n *naiveKDC, o genOp) refResult {
	t.Helper()
	switch o.kind {
	case kindIssue:
		return n.issueTGT(o.subject, o.start, o.till, o.renewTill, o.now)
	case kindTGS:
		return n.tgs(o.id, o.service, o.till, o.renewTill, o.now)
	case kindRenew:
		return n.renew(o.id, o.now)
	case kindValidate:
		return n.validate(o.id, o.now)
	case kindAuth:
		return n.authenticate(o.id, o.authTime, o.now)
	default:
		return n.changeKey(o.subject, o.now)
	}
}

func runReal(t *testing.T, k *KDC, o genOp) (string, *Ticket) {
	t.Helper()
	switch o.kind {
	case kindIssue:
		tk, err := k.IssueTGT([]byte(o.subject), o.start, o.till, o.renewTill, o.now)
		return reasonOf(err), tk
	case kindTGS:
		tk, err := k.TGS(o.id, []byte(o.service), o.till, o.renewTill, o.now)
		return reasonOf(err), tk
	case kindRenew:
		tk, err := k.Renew(o.id, o.now)
		return reasonOf(err), tk
	case kindValidate:
		tk, err := k.Validate(o.id, o.now)
		return reasonOf(err), tk
	case kindAuth:
		err := k.Authenticate(o.id, o.authTime, o.now)
		return reasonOf(err), nil
	default:
		err := k.ChangeKey([]byte(o.subject), o.now)
		return reasonOf(err), nil
	}
}

func ticketString(tk *Ticket) string {
	if tk == nil {
		return "<nil>"
	}
	return fmt.Sprintf("t%d subj=%q svc=%q tgt=%v [%d,%d) rt=%d invalid=%v issued=%d",
		tk.ID, tk.Subject, tk.Service, tk.IsTGT, tk.Start, tk.End,
		tk.RenewTill, tk.Invalid, tk.Issued)
}

func refString(tk *refTicket) string {
	if tk == nil {
		return "<nil>"
	}
	return fmt.Sprintf("t%d subj=%q svc=%q tgt=%v [%d,%d) rt=%d invalid=%v issued=%d",
		tk.id, tk.subject, tk.service, tk.isTGT, tk.start, tk.end,
		tk.renewTill, tk.invalid, tk.issued)
}

func statesEqual(k *KDC, n *naiveKDC) string {
	if k.nextID != n.nextID {
		return fmt.Sprintf("nextID: real=%d naive=%d", k.nextID, n.nextID)
	}
	if k.maxNow != n.maxNow {
		return fmt.Sprintf("maxNow: real=%d naive=%d", k.maxNow, n.maxNow)
	}
	var ids []int64
	for id := range n.tickets {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		tk, err := k.ticket(id)
		if err != nil {
			return fmt.Sprintf("real missing t%d", id)
		}
		rt := n.tickets[id]
		if tk.Start != rt.start || tk.End != rt.end || tk.RenewTill != rt.renewTill ||
			tk.Invalid != rt.invalid || tk.Issued != rt.issued ||
			string(tk.Subject) != rt.subject || string(tk.Service) != rt.service ||
			tk.IsTGT != rt.isTGT {
			return fmt.Sprintf("ticket mismatch:\n  real : %s\n  naive: %s", ticketString(tk), refString(rt))
		}
	}
	// replay cache contents must match
	realEntries := map[refEntry]bool{}
	for e := range k.cache.m {
		realEntries[refEntry{e.id, e.time}] = true
	}
	for e := range n.cache {
		if !realEntries[e] {
			return fmt.Sprintf("naive cache has %v but real does not", e)
		}
		delete(realEntries, e)
	}
	for e := range realEntries {
		return fmt.Sprintf("real cache has %v but naive does not", e)
	}
	return ""
}

func TestDifferentialAgainstNaive(t *testing.T) {
	total := 0
	rng := rand.New(rand.NewSource(20261002))
	for seq := 0; total < 2000; seq++ {
		L := int64(1 + rng.Intn(200))
		R := L + int64(rng.Intn(2000))
		S := int64(1 + rng.Intn(30))
		P := int64(1 + rng.Intn(100))
		k, err1 := NewKDC(L, R, S, P)
		n, err2 := newNaive(L, R, S, P)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("config disagreement for %d %d %d %d", L, R, S, P)
		}
		ops := genSeq(rng, 1+seq%40, L, R, S, P)
		var logBuf []string
		logBuf = append(logBuf, fmt.Sprintf("=== sequence %d: L=%d R=%d S=%d P=%d, %d ops ===",
			seq, L, R, S, P, len(ops)))
		for i, o := range ops {
			before := k.cache.popCount
			expiredBefore := 0
			for _, expiry := range n.cache {
				if o.now > expiry {
					expiredBefore++
				}
			}
			rr := runNaive(t, n, o)
			gotReason, gotTicket := runReal(t, k, o)
			nline := ""
			if len(n.log) > 0 {
				nline = n.log[len(n.log)-1]
			}
			logBuf = append(logBuf, nline)
			if gotReason != rr.reason {
				logBuf = append(logBuf, fmt.Sprintf("  REASON MISMATCH op#%d real=%q naive=%q",
					i, gotReason, rr.reason))
				t.Fatalf("seq %d op %d: reason real=%q naive=%q\n%s",
					seq, i, gotReason, rr.reason, strings.Join(logBuf, "\n"))
			}
			if rr.reason == "" {
				if rr.ticket != nil {
					if gotTicket == nil {
						t.Fatalf("seq %d op %d: real returned no ticket", seq, i)
					}
					if gotTicket.ID != rr.ticket.id {
						t.Fatalf("seq %d op %d: id real=t%d naive=t%d",
							seq, i, gotTicket.ID, rr.ticket.id)
					}
				}
				if diff := statesEqual(k, n); diff != "" {
					t.Fatalf("seq %d op %d state mismatch: %s\n%s",
						seq, i, diff, strings.Join(logBuf, "\n"))
				}
				// amortized heap pops: <= number of entries due at now + 1
				pops := k.cache.popCount - before
				if pops > int64(expiredBefore)+1 {
					t.Fatalf("seq %d op %d: pops=%d expired=%d (amortized bound violated)",
						seq, i, pops, expiredBefore)
				}
			}
			total++
		}
		// print full input/output/rationale log for every sequence in -v mode
		for _, line := range logBuf {
			t.Log(line)
		}
		n.log = nil
		if total >= 2000 {
			break
		}
	}
	t.Logf("differential operations checked: %d", total)
}
