package mileage

import (
	"fmt"
	"math/rand"
	"testing"
)

type naiveAccount struct {
	openedAt, lastClockAt, lastActivityAt int64
	frozen                                bool
	level, currentPeriod                  int64
	periodMiles                           map[int64]int
	balance, debt                         int
	segments                              map[string]naiveSegment
	redemptions                           map[string]naiveRedemption
}

type naiveSegment struct {
	flownAt            int64
	qualifying, redeem int
	posted, known      bool
}

type naiveRedemption struct {
	cost     int
	canceled bool
}

type naiveModel struct {
	config Config
	accts  map[string]*naiveAccount
}

func newNaiveModel(c Config) *naiveModel {
	return &naiveModel{config: c, accts: map[string]*naiveAccount{}}
}

func (m *naiveModel) acct(id string) *naiveAccount {
	if a := m.accts[id]; a != nil {
		return a
	}
	a := &naiveAccount{periodMiles: map[int64]int{}, segments: map[string]naiveSegment{}, redemptions: map[string]naiveRedemption{}}
	m.accts[id] = a
	return a
}

func (m *naiveModel) roll(a *naiveAccount, now int64) {
	want := (now - a.openedAt) / m.config.PeriodSeconds
	for a.currentPeriod < want {
		ended := a.currentPeriod
		pl := int64(levelForMiles(m.config.Thresholds, a.periodMiles[ended]))
		floor := a.level - 1
		if floor < 0 {
			floor = 0
		}
		if pl > floor {
			a.level = pl
		} else {
			a.level = floor
		}
		a.currentPeriod++
	}
}

func (m *naiveModel) frozen(a *naiveAccount, now int64) bool {
	return a.frozen || now-a.lastActivityAt >= m.config.InactivitySeconds
}

func (m *naiveModel) cash(a *naiveAccount, amount int) (repaid, added int) {
	if amount >= a.debt {
		repaid, added = a.debt, amount-a.debt
		a.debt, a.balance = 0, a.balance+added
	} else {
		repaid, a.debt = amount, a.debt-amount
	}
	return
}

func (m *naiveModel) create(id string, now int64) error {
	if id == "" || now < 0 {
		return ErrInvalidArgument
	}
	if _, ok := m.accts[id]; ok {
		return ErrInvalidArgument
	}
	a := m.acct(id)
	a.openedAt, a.lastClockAt, a.lastActivityAt = now, now, now
	return nil
}

func (m *naiveModel) credit(in CreditInput) (CreditResult, error) {
	s := in.Segment
	bad := in.AccountID == "" || in.Now < 0 || s.ID == "" || s.Distance <= 0 ||
		s.FarePercent < 0 || s.FarePercent > 300 || s.FlownAt < 0
	if bad {
		return CreditResult{}, ErrInvalidArgument
	}
	a, ok := m.accts[in.AccountID]
	if !ok {
		return CreditResult{}, ErrAccountNotFound
	}
	if in.Now < a.lastClockAt {
		return CreditResult{}, ErrClockRewind
	}
	if m.frozen(a, in.Now) {
		return CreditResult{}, ErrAccountFrozen
	}
	if _, ok := a.segments[s.ID]; ok {
		return CreditResult{}, ErrDuplicateCredit
	}
	if in.Now > s.FlownAt+m.config.RetroWindowSeconds {
		return CreditResult{}, ErrLateCredit
	}
	if s.FlownAt < a.openedAt {
		return CreditResult{}, ErrInvalidArgument
	}

	m.roll(a, in.Now)
	base := (s.Distance*s.FarePercent + 99) / 100
	if base < m.config.MinimumBaseMiles {
		base = m.config.MinimumBaseMiles
	}
	before := int(a.level)
	gross := base + base*m.config.BonusPercent[before]/100
	repaid, added := m.cash(a, gross)
	period := (s.FlownAt - a.openedAt) / m.config.PeriodSeconds
	a.periodMiles[period] += base
	after := int64(before)
	if period == a.currentPeriod {
		after = int64(levelForMiles(m.config.Thresholds, a.periodMiles[period]))
		if after < a.level {
			after = a.level
		}
		a.level = after
	}
	a.segments[s.ID] = naiveSegment{flownAt: s.FlownAt, qualifying: base, redeem: gross, posted: true, known: true}
	a.lastClockAt, a.lastActivityAt, a.frozen = in.Now, in.Now, false
	return CreditResult{period, before, int(after), base, base, gross, repaid, added}, nil
}

func (m *naiveModel) refund(in RefundInput) RefundResult {
	a, ok := m.accts[in.AccountID]
	if !ok || in.Now < a.lastClockAt {
		return RefundResult{}
	}
	m.roll(a, in.Now)
	r, seen := a.segments[in.SegmentID]
	if !seen {
		a.segments[in.SegmentID] = naiveSegment{known: true}
		a.lastClockAt = in.Now
		return RefundResult{}
	}
	res := RefundResult{Seen: true, Period: (r.flownAt - a.openedAt) / m.config.PeriodSeconds}
	if !r.posted {
		a.lastClockAt = in.Now
		return res
	}
	r.posted = false
	res.Posted, res.QualifyingMiles, res.RedeemableMiles = true, r.qualifying, r.redeem
	a.periodMiles[res.Period] -= r.qualifying
	if a.balance >= r.redeem {
		a.balance -= r.redeem
	} else {
		res.DebtCreated = r.redeem - a.balance
		a.debt += res.DebtCreated
		a.balance = 0
	}
	a.segments[in.SegmentID] = r
	a.lastClockAt = in.Now
	return res
}

func (m *naiveModel) redeem(in RedeemInput) (RedeemResult, error) {
	if in.AccountID == "" || in.Now < 0 || in.RedemptionID == "" || in.CostMiles <= 0 {
		return RedeemResult{}, ErrInvalidArgument
	}
	a, ok := m.accts[in.AccountID]
	if !ok {
		return RedeemResult{}, ErrAccountNotFound
	}
	if in.Now < a.lastClockAt {
		return RedeemResult{}, ErrClockRewind
	}
	if m.frozen(a, in.Now) {
		return RedeemResult{}, ErrAccountFrozen
	}
	if _, ok := a.redemptions[in.RedemptionID]; ok {
		return RedeemResult{}, ErrInvalidArgument
	}
	if a.balance < in.CostMiles {
		return RedeemResult{}, ErrInsufficientMiles
	}
	m.roll(a, in.Now)
	before := a.balance
	a.balance -= in.CostMiles
	a.redemptions[in.RedemptionID] = naiveRedemption{cost: in.CostMiles}
	a.lastClockAt, a.lastActivityAt, a.frozen = in.Now, in.Now, false
	return RedeemResult{before, a.balance}, nil
}

func (m *naiveModel) cancel(in CancelRedemptionInput) (CancelRedemptionResult, error) {
	if in.AccountID == "" || in.Now < 0 || in.RedemptionID == "" {
		return CancelRedemptionResult{}, ErrInvalidArgument
	}
	a, ok := m.accts[in.AccountID]
	if !ok {
		return CancelRedemptionResult{}, ErrAccountNotFound
	}
	if in.Now < a.lastClockAt {
		return CancelRedemptionResult{}, ErrClockRewind
	}
	if m.frozen(a, in.Now) {
		return CancelRedemptionResult{}, ErrAccountFrozen
	}
	r, found := a.redemptions[in.RedemptionID]
	if !found {
		return CancelRedemptionResult{}, ErrRedemptionNotFound
	}
	if r.canceled {
		return CancelRedemptionResult{}, ErrRedemptionCanceled
	}
	m.roll(a, in.Now)
	r.canceled = true
	a.redemptions[in.RedemptionID] = r
	refund := r.cost - m.config.CancelFeeMiles
	if refund < 0 {
		refund = 0
	}
	repaid, added := m.cash(a, refund)
	a.lastClockAt = in.Now
	return CancelRedemptionResult{r.cost, refund, repaid, added}, nil
}

func (m *naiveModel) unfreeze(in UnfreezeInput) error {
	if in.AccountID == "" || in.Now < 0 {
		return ErrInvalidArgument
	}
	a, ok := m.accts[in.AccountID]
	if !ok {
		return ErrAccountNotFound
	}
	if in.Now < a.lastClockAt {
		return ErrClockRewind
	}
	m.roll(a, in.Now)
	a.frozen = false
	a.lastClockAt, a.lastActivityAt = in.Now, in.Now
	return nil
}

func (m *naiveModel) view(id string, now int64) AccountView {
	a := m.accts[id]
	m.roll(a, now)
	return AccountView{id, int(a.level), a.periodMiles[a.currentPeriod], a.balance, a.debt,
		m.frozen(a, now), a.currentPeriod, a.lastClockAt}
}

type randomOp struct {
	kind                    int
	now, flownAt            int64
	segID, redID            string
	distance, percent, cost int
}

func randomConfig() Config {
	return Config{5, 8, 10, [3]int{80, 160, 260}, [4]int{0, 10, 25, 40}, 18, 7}
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	if !testing.Verbose() {
		t.Log("rerun with -v to print every step")
	}
	config := randomConfig()
	for seq := 0; seq < 80; seq++ {
		r := rand.New(rand.NewSource(int64(1000 + seq)))
		service, _ := NewService(config)
		model := newNaiveModel(config)
		if err := service.CreateAccount("a", 0); err != nil || model.create("a", 0) != nil {
			t.Fatal("setup")
		}
		var lastNow int64
		for step := 0; step < 180; step++ {
			op := randomOp{
				kind: r.Intn(6), now: r.Int63n(41),
				segID: fmt.Sprintf("s%d", r.Intn(14)), redID: fmt.Sprintf("r%d", r.Intn(8)),
				distance: r.Intn(120) + 1, percent: r.Intn(301),
				flownAt: r.Int63n(41), cost: r.Intn(160) + 1,
			}
			if step%5 == 0 {
				op.now = int64(step/5) * 10
			}
			if op.now < lastNow {
				op.now = lastNow
			}
			lastNow = op.now
			if op.flownAt > op.now {
				op.flownAt = op.now
			}
			input := fmt.Sprintf("seq=%d step=%d op=%+v", seq, step, op)
			switch op.kind {
			case 0:
				in := CreditInput{
					AccountID: "a",
					Now:       op.now,
					Segment: Segment{
						ID: op.segID, Distance: op.distance,
						FarePercent: op.percent, FlownAt: op.flownAt,
					},
				}
				got, ge := service.Credit(in)
				want, we := model.credit(in)
				reason := decisionReason(ge)
				if ge == nil {
					reason = fmt.Sprintf("accepted period=%d base=%d gross=%d debtRepaid=%d added=%d level=%d->%d",
						got.Period, got.BaseMiles, got.GrossRedeemable, got.DebtRepaid,
						got.RedeemableAdded, got.LevelBefore, got.LevelAfter)
				}
				t.Logf("%s decision=%+v reason=%s", input, got, reason)
				assertSameError(t, ge, we)
				if ge == nil && got != want {
					t.Fatalf("credit got=%+v want=%+v", got, want)
				}
			case 1:
				in := RefundInput{AccountID: "a", Now: op.now, SegmentID: op.segID}
				got, _ := service.Refund(in)
				want := model.refund(in)
				t.Logf("%s decision=%+v reason=refund accepted even when unknown or frozen", input, got)
				if got != want {
					t.Fatalf("refund got=%+v want=%+v", got, want)
				}
			case 2:
				in := RedeemInput{AccountID: "a", RedemptionID: op.redID, CostMiles: op.cost, Now: op.now}
				got, ge := service.Redeem(in)
				want, we := model.redeem(in)
				t.Logf("%s decision=%+v reason=%s", input, got, decisionReason(ge))
				assertSameError(t, ge, we)
				if ge == nil && got != want {
					t.Fatalf("redeem got=%+v want=%+v", got, want)
				}
			case 3:
				in := CancelRedemptionInput{AccountID: "a", RedemptionID: op.redID, Now: op.now}
				got, ge := service.CancelRedemption(in)
				want, we := model.cancel(in)
				t.Logf("%s decision=%+v reason=%s", input, got, decisionReason(ge))
				assertSameError(t, ge, we)
				if ge == nil && got != want {
					t.Fatalf("cancel got=%+v want=%+v", got, want)
				}
			case 4:
				in := UnfreezeInput{AccountID: "a", Now: op.now}
				ge, we := service.Unfreeze(in), model.unfreeze(in)
				t.Logf("%s decision=unfreeze reason=%s", input, decisionReason(ge))
				assertSameError(t, ge, we)
			default:
				got, _ := service.ViewAt("a", op.now)
				want := model.view("a", op.now)
				t.Logf("%s decision=%+v reason=read-only projection at operation time", input, got)
				if got != want {
					t.Fatalf("view got=%+v want=%+v", got, want)
				}
			}
		}
	}
}

func assertSameError(t *testing.T, got, want error) {
	t.Helper()
	if got != want {
		t.Fatalf("error got=%v want=%v", got, want)
	}
}

func decisionReason(err error) string {
	switch err {
	case nil:
		return "accepted"
	case ErrInvalidArgument:
		return "rejected: invalid argument"
	case ErrClockRewind:
		return "rejected: clock rewind"
	case ErrAccountNotFound:
		return "rejected: account missing"
	case ErrAccountFrozen:
		return "rejected: account frozen"
	case ErrDuplicateCredit:
		return "rejected: duplicate credit"
	case ErrLateCredit:
		return "rejected: retroactive deadline"
	case ErrRedemptionNotFound:
		return "rejected: redemption missing"
	case ErrRedemptionCanceled:
		return "rejected: redemption canceled"
	case ErrInsufficientMiles:
		return "rejected: insufficient miles"
	default:
		return "rejected: " + err.Error()
	}
}
