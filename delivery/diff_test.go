package delivery

import (
	"fmt"
	"math/rand"
	"testing"
)

// modelIface is the smallest surface shared by Platform and NaiveModel.
type modelIface interface {
	CreateOrder(id string, t int, addr Address, disp Disposition) (*Order, error)
	Pickup(id string, t int) (*Order, error)
	Deliver(id string, t int) (*Order, error)
	ReportUnreachable(id string, t int) (*Exception, error)
	ReportWrongAddress(id string, t int) (*Exception, error)
	ReportRefusal(id string, t int, evID string, evTime int) (*Exception, error)
	RecordContact(id string, t int) (*Exception, error)
	CustomerRespond(id string, t int) (*Order, error)
	JudgeUndeliverable(id string, t int) (*Order, error)
	SubmitCorrection(id string, t int, addr Address) (*Order, error)
	RiderReturn(id string, t int) (*Order, error)
	MerchantConfirm(id string, t int) (*Order, error)
	Get(id string, t int) (*Order, error)
}

var (
	_ modelIface = (*Platform)(nil)
	_ modelIface = (*NaiveModel)(nil)
)

type stepResult struct {
	order *Order
	exc   *Exception
	err   ErrorCode
}

func runStep(m modelIface, op int, id string, t int, addr Address, evT int) stepResult {
	switch op {
	case 0:
		_, e := m.CreateOrder(id, t, Address{}, DispositionOnSite)
		return stepResult{err: errCode(e)}
	case 1:
		_, e := m.CreateOrder(id, t, Address{}, DispositionReturn)
		return stepResult{err: errCode(e)}
	case 2:
		o, e := m.Pickup(id, t)
		return stepResult{order: o, err: errCode(e)}
	case 3:
		o, e := m.Deliver(id, t)
		return stepResult{order: o, err: errCode(e)}
	case 4:
		ex, e := m.ReportUnreachable(id, t)
		return stepResult{exc: ex, err: errCode(e)}
	case 5:
		ex, e := m.ReportWrongAddress(id, t)
		return stepResult{exc: ex, err: errCode(e)}
	case 6:
		ex, e := m.ReportRefusal(id, t, "ev", evT)
		return stepResult{exc: ex, err: errCode(e)}
	case 7:
		ex, e := m.RecordContact(id, t)
		return stepResult{exc: ex, err: errCode(e)}
	case 8:
		o, e := m.CustomerRespond(id, t)
		return stepResult{order: o, err: errCode(e)}
	case 9:
		o, e := m.JudgeUndeliverable(id, t)
		return stepResult{order: o, err: errCode(e)}
	case 10:
		o, e := m.SubmitCorrection(id, t, addr)
		return stepResult{order: o, err: errCode(e)}
	case 11:
		o, e := m.RiderReturn(id, t)
		return stepResult{order: o, err: errCode(e)}
	case 12:
		o, e := m.MerchantConfirm(id, t)
		return stepResult{order: o, err: errCode(e)}
	default:
		o, e := m.Get(id, t)
		return stepResult{order: o, err: errCode(e)}
	}
}

var opNames = []string{
	"CreateOnSite", "CreateReturn", "Pickup", "Deliver",
	"ReportUnreachable", "ReportWrongAddress", "ReportRefusal",
	"RecordContact", "CustomerRespond", "JudgeUndeliverable",
	"SubmitCorrection", "RiderReturn", "MerchantConfirm", "Get",
}

func canonOrder(o *Order) string {
	if o == nil {
		return "<nil>"
	}
	ex := "<none>"
	if o.Current != nil {
		ex = fmt.Sprintf("{seq=%d typ=%d phase=%d start=%d cnt=%d end=%d}",
			o.Current.ID, o.Current.Type, o.Current.Phase,
			o.Current.Start, o.Current.ContactCount, o.Current.WindowEnd)
	}
	return fmt.Sprintf("st=%d addr=%d,%d disp=%d liable=%d changes=%d comp=%v seq=%d win=%d ex=%s",
		o.Status, o.Address.X, o.Address.Y, o.Disposition, o.Liability,
		o.AddrChanges, o.CompPaid, o.ExceptionSeq, o.WindowEnd, ex)
}

func canonExc(e *Exception) string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("{id=%d typ=%d phase=%d start=%d cnt=%d end=%d}",
		e.ID, e.Type, e.Phase, e.Start, e.ContactCount, e.WindowEnd)
}

func randomConfig(r *rand.Rand) Config {
	return Config{
		MinWaitSeconds:        r.Intn(20),
		MinContacts:           r.Intn(4),
		MinContactInterval:    r.Intn(5),
		CorrectionWindow:      1 + r.Intn(25),
		MaxCorrectionDist:     r.Intn(6),
		EvidenceTTLSeconds:    r.Intn(20),
		RiderCompensation:     5 + r.Intn(20),
		MerchantConfirmWindow: 1 + r.Intn(25),
	}
}

// TestRandomDifferential replays identical random operation streams through
// both implementations and requires identical answers and snapshots after
// every step. Each step logs input, outputs and the deciding rule.
func TestRandomDifferential(t *testing.T) {
	const iterations = 1500
	for iter := 0; iter < iterations; iter++ {
		r := rand.New(rand.NewSource(int64(1000 + iter)))
		cfg := randomConfig(r)
		p, err1 := NewPlatform(cfg)
		n, err2 := NewNaiveModel(cfg)
		if err1 != nil || err2 != nil {
			t.Fatalf("construct: %v %v", err1, err2)
		}
		now := 0
		id := fmt.Sprintf("ord-%d", iter)
		t.Logf("==== iter %d cfg=%+v order=%s ====", iter, cfg, id)
		steps := 30 + r.Intn(40)
		for st := 0; st < steps; st++ {
			op := r.Intn(len(opNames))
			// Mostly advance time by small nondecreasing jumps; occasionally
			// jump forward to trigger window maturation.
			if r.Intn(6) == 0 {
				now += r.Intn(30)
			} else {
				now += r.Intn(4)
			}
			addr := Address{X: r.Intn(8), Y: r.Intn(8)}
			evT := now - r.Intn(cfg.EvidenceTTLSeconds+3)

			gotP := runStep(p, op, id, now, addr, evT)
			gotN := runStep(n, op, id, now, addr, evT)

			// Create ops use the same id every time; only the first create
			// can succeed, after which duplicates diverge from "not found".
			// That is an intended input artifact, not a model difference.
			reason := decideReason(cfg, op, now, gotP.err)
			t.Logf("step %3d t=%3d %-18s addr=%v evT=%d => platform(err=%d,%s,%s) naive(err=%d,%s,%s) | %s",
				st, now, opNames[op], addr, evT,
				gotP.err, canonOrder(gotP.order), canonExc(gotP.exc),
				gotN.err, canonOrder(gotN.order), canonExc(gotN.exc), reason)

			if gotP.err != gotN.err {
				t.Fatalf("iter %d step %d %s t=%d: error code platform=%d naive=%d",
					iter, st, opNames[op], now, gotP.err, gotN.err)
			}
			if canonOrder(gotP.order) != canonOrder(gotN.order) {
				t.Fatalf("iter %d step %d %s t=%d:\n platform %s\n naive    %s",
					iter, st, opNames[op], now,
					canonOrder(gotP.order), canonOrder(gotN.order))
			}
			if canonExc(gotP.exc) != canonExc(gotN.exc) {
				t.Fatalf("iter %d step %d %s t=%d:\n platform %s\n naive    %s",
					iter, st, opNames[op], now,
					canonExc(gotP.exc), canonExc(gotN.exc))
			}
		}
	}
}

// decideReason renders the human-readable deciding rule for the log.
func decideReason(cfg Config, op, t int, code ErrorCode) string {
	if code != 0 {
		return "rejected: " + codeName(code)
	}
	return "accepted: " + opNames[op] + " @t=" + fmt.Sprint(t)
}

func codeName(c ErrorCode) string {
	switch c {
	case ErrInvalidParam:
		return "invalid param"
	case ErrClockBack:
		return "clock regression"
	case ErrOrderNotFound:
		return "order not found"
	case ErrExceptionNotFound:
		return "exception not found"
	case ErrNotPickedUp:
		return "not picked up"
	case ErrAlreadyDelivered:
		return "already delivered"
	case ErrExceptionClosed:
		return "exception closed"
	case ErrOrderTerminal:
		return "order terminal"
	case ErrActiveException:
		return "active exception"
	case ErrTypeMismatch:
		return "type/status mismatch"
	case ErrEvidenceInvalid:
		return "evidence invalid"
	case ErrContactTooFrequent:
		return "contact too frequent"
	case ErrConditionWait:
		return "wait condition missing"
	case ErrConditionContact:
		return "contact condition missing"
	case ErrCorrectionWindow:
		return "correction window expired"
	case ErrConfirmWindow:
		return "confirm window expired"
	case ErrDistanceExceeded:
		return "distance exceeded"
	}
	return "unknown"
}
