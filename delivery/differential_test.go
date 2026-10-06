package delivery

import (
	"fmt"
	"math/rand"
	"testing"
)

type opKind int

const (
	kPickup opKind = iota
	kDeliver
	kReport
	kContact
	kJudge
	kRespond
	kCorrect
	kReturn
	kConfirm
)

type step struct {
	kind opKind
	at   int64
	ord  int
	et   ExceptionType
	x, y int64
	evID string
	evAt int64
	exc  string // 入参异常 ID
}

type model interface {
	PlaceOrder(id string, disp Disposition, x, y, at int64) error
	PickUp(id string, at int64) error
	ConfirmDelivery(id string, at int64) error
	ReportException(orderID string, at int64, et ExceptionType, evID string, evAt int64) (string, error)
	RecordContact(id string, at int64) error
	JudgeUndeliverable(id string, at int64) error
	UserRespond(id string, at int64) error
	SubmitCorrection(id string, x, y, at int64) error
	RiderReturn(id string, at int64) error
	MerchantConfirm(id string, at int64) error
}

func runStep(m model, st step) (string, error) {
	id := fmt.Sprintf("o%d", st.ord)
	switch st.kind {
	case kPickup:
		return "", m.PickUp(id, st.at)
	case kDeliver:
		return "", m.ConfirmDelivery(id, st.at)
	case kReport:
		return m.ReportException(id, st.at, st.et, st.evID, st.evAt)
	case kContact:
		return "", m.RecordContact(st.exc, st.at)
	case kJudge:
		return "", m.JudgeUndeliverable(st.exc, st.at)
	case kRespond:
		return "", m.UserRespond(st.exc, st.at)
	case kCorrect:
		return "", m.SubmitCorrection(st.exc, st.x, st.y, st.at)
	case kReturn:
		return "", m.RiderReturn(id, st.at)
	case kConfirm:
		return "", m.MerchantConfirm(id, st.at)
	}
	return "", nil
}

func fastSnapshot(s *System, at int64) (map[string]OrderView, map[string]ExceptionView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ov := map[string]OrderView{}
	for id, o := range s.orders {
		s.settleAt(o, at)
		ov[id] = o.view
	}
	ev := map[string]ExceptionView{}
	for id, ex := range s.excs {
		ev[id] = ex.view
	}
	return ov, ev
}

// TestDifferentialAgainstNaive 将随机操作序列同时施加于快速实现与朴素事件溯源模型，
// 每步比对错误码、异常 ID 与全量快照；-v 时打印每步输入、输出与判定依据。
func TestDifferentialAgainstNaive(t *testing.T) {
	for _, seed := range []int64{1, 2, 7, 42, 99} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			fast, err := NewSystem(testParams())
			if err != nil {
				t.Fatal(err)
			}
			naive := NewNaive(testParams())
			const nOrders = 6
			for i := 0; i < nOrders; i++ {
				disp := Disposition(i % 2)
				if err := fast.PlaceOrder(fmt.Sprintf("o%d", i), disp, 0, 0, 0); err != nil {
					t.Fatal(err)
				}
				if err := naive.PlaceOrder(fmt.Sprintf("o%d", i), disp, 0, 0, 0); err != nil {
					t.Fatal(err)
				}
			}
			// 已知异常 ID 池（ID 由成功上报顺序决定，两模型一致）。
			var excPool []string
			var now int64
			for i := 0; i < 1500; i++ {
				now += int64(rng.Intn(4)) // 允许相邻时刻相等，制造同时刻交错
				ord := rng.Intn(nOrders)
				st := step{kind: opKind(rng.Intn(int(kConfirm) + 1)), at: now, ord: ord}
				switch st.kind {
				case kReport:
					st.et = ExceptionType(rng.Intn(3))
					if st.et == ETRejection {
						st.evID = fmt.Sprintf("ev-%d-%d", seed, i)
						st.evAt = now // 证据时刻=上报时刻，始终有效
					}
				case kContact, kJudge, kRespond:
					if len(excPool) == 0 {
						st.kind = kReport
						st.et = ETUnreachable
					} else {
						st.exc = excPool[rng.Intn(len(excPool))]
					}
				case kCorrect:
					if len(excPool) == 0 {
						st.kind = kReport
						st.et = ETWrongAddress
					} else {
						st.exc = excPool[rng.Intn(len(excPool))]
						// 覆盖恰等、窗口内、超限三类。
						d := rng.Intn(20)
						st.x = int64(d)
					}
				}

				idF, errF := runStep(fast, st)
				idN, errN := runStep(naive, st)
				cf, cn := CodeOf(errF), CodeOf(errN)
				t.Logf("seed=%d step=%4d %s at=%d ord=%d exc=%s => fast=(%s,%v) naive=(%s,%v)",
					seed, i, kindName(st.kind), st.at, st.ord, st.exc, idF, codeName(cf), idN, codeName(cn))
				if cf != cn {
					t.Fatalf("code mismatch at step %d: fast=%s naive=%s, step=%+v errF=%v errN=%v",
						i, codeName(cf), codeName(cn), st, errF, errN)
				}
				if cf == ErrOK {
					if idF != idN {
						t.Fatalf("exception id mismatch at %d: %s vs %s", i, idF, idN)
					}
					if idF != "" {
						excPool = append(excPool, idF)
					}
				}
				fo, fe := fastSnapshot(fast, st.at)
				no, ne := naive.Snapshot(st.at)
				if !snapOrdersEqual(fo, no) || !snapExcsEqual(fe, ne) {
					t.Fatalf("snapshot mismatch at step %d, step=%+v\nfast orders=%#v\nnaive orders=%#v\nfast exc=%#v\nnaive exc=%#v",
						i, st, fo, no, fe, ne)
				}
			}
		})
	}
}

func snapOrdersEqual(a, b map[string]OrderView) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if v != b[k] {
			return false
		}
	}
	return true
}

func snapExcsEqual(a, b map[string]ExceptionView) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if v != b[k] {
			return false
		}
	}
	return true
}

func kindName(k opKind) string {
	names := []string{"pickup", "deliver", "report", "contact", "judge", "respond", "correct", "return", "confirm"}
	return names[k]
}

func codeName(c ErrCode) string {
	names := map[ErrCode]string{
		ErrOK: "OK", ErrInvalidParam: "INVALID_PARAM", ErrClockRollback: "CLOCK_ROLLBACK",
		ErrOrderNotFound: "ORDER_NOT_FOUND", ErrExceptionNotFound: "EXC_NOT_FOUND",
		ErrNotPickedUp: "NOT_PICKED", ErrAlreadyDelivered: "ALREADY_DELIVERED",
		ErrExceptionClosed: "EXC_CLOSED", ErrOrderTerminal: "ORDER_TERMINAL",
		ErrActiveException: "ACTIVE_EXC", ErrTypeMismatch: "TYPE_MISMATCH",
		ErrWrongState: "WRONG_STATE", ErrInvalidEvidence: "INVALID_EVIDENCE",
		ErrContactTooSoon: "CONTACT_TOO_SOON", ErrConditionWait: "COND_WAIT",
		ErrConditionContact: "COND_CONTACT", ErrCorrectionWindow: "CORR_WINDOW",
		ErrConfirmWindow: "CONFIRM_WINDOW", ErrDistanceExceeded: "DISTANCE",
	}
	if n, ok := names[c]; ok {
		return n
	}
	return "?"
}
