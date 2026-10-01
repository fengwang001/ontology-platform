// Package billing implements a subscription billing engine that prorates
// mid-cycle plan changes day by day.
//
// A billing period has D days. On day 0 the current plan's full list price is
// charged in advance. Every change on day day (0 < day < D) emits two lines:
// a refund of the old plan for the D-day remaining days, rounded down, and a
// charge of the new plan for the same remaining days, rounded up. Settle
// closes the period, returns all of its lines, and opens a new one whose day-0
// line prepays the then-current plan.
package billing

import (
	"sort"
	"sync"
)

// MaxPrice is the inclusive upper bound of a plan's full-period list price,
// in cents.
const MaxPrice int64 = 1_000_000_000_000

// LineKind identifies the role of a billing line.
type LineKind int

const (
	// LineCharge is money owed to the merchant (prepayment or prorated
	// change charge); its Amount is non-negative.
	LineCharge LineKind = iota
	// LineRefund is money returned to the subscriber; its Amount is
	// non-positive.
	LineRefund
)

// Reason classifies a rejected operation.
type Reason int

const (
	ReasonUnknown Reason = iota
	// ReasonInvalidPeriodDays: D is not in [1, 366].
	ReasonInvalidPeriodDays
	// ReasonInvalidPrice: a plan price is not in [0, 1e12].
	ReasonInvalidPrice
	// ReasonUnknownPlan: the named plan does not exist.
	ReasonUnknownPlan
	// ReasonSamePlan: the target plan equals the current plan.
	ReasonSamePlan
	// ReasonDayOutOfRange: day is not in [1, D-1].
	ReasonDayOutOfRange
	// ReasonDayOutOfOrder: day is before the previous change day.
	ReasonDayOutOfOrder
	// ReasonDayRepeated: day equals the previous change day.
	ReasonDayRepeated
)

// Error is returned for every rejected operation. It carries the machine
// readable Reason so callers can distinguish the failure causes.
type Error struct {
	Op     string
	Reason Reason
	detail string
}

func (e *Error) Error() string {
	return "billing: " + e.Op + ": " + e.detail
}

// Line is one immutable billing row within a period.
type Line struct {
	// Period is the 1-based billing period the line belongs to.
	Period int
	// Seq is the 1-based sequence number of the line within its period.
	Seq int
	// Kind is LineCharge or LineRefund.
	Kind LineKind
	// Day is the day on which the line takes effect (0 for the prepay line).
	Day int
	// Plan is the plan whose list price the line is computed from.
	Plan string
	// Price is that plan's full-period list price, in cents.
	Price int64
	// Remain is the number of remaining days used for proration (D - Day).
	Remain int
	// Amount is the signed amount in cents (refunds are negative).
	Amount int64
}

// Snapshot is a point-in-time, copy-on-read view of an engine.
type Snapshot struct {
	// Period is the 1-based number of the current open period.
	Period int
	// Days is D, the number of days in a period.
	Days int
	// CurrentPlan is the plan in effect right now.
	CurrentPlan string
	// LastDay is the day of the last change in the current period, or -1 if
	// the period has no change yet.
	LastDay int
	// Lines are the lines generated in the current period so far.
	Lines []Line
	// Total is the algebraic sum of Amount over Lines.
	Total int64
}

// Engine is a concurrency-safe billing engine. Change, Settle and Query may be
// called concurrently; the observed result is always equivalent to some
// serial ordering of the calls.
type Engine struct {
	mu sync.Mutex

	days    int
	prices  map[string]int64
	period  int
	current string
	lastDay int
	lines   []Line
	total   int64
}

// New creates an engine with a D-day period, the plan price table and the
// initially selected plan. The first period is opened immediately and its
// day-0 prepay line is generated.
func New(D int, prices map[string]int64, initial string) (*Engine, error) {
	if D < 1 || D > 366 {
		return nil, &Error{Op: "new", Reason: ReasonInvalidPeriodDays,
			detail: "period days D out of range [1,366]: " + itoa(D)}
	}
	// Validate prices in a fixed (sorted) order so that replay produces
	// identical error messages even when several entries are invalid.
	names := make([]string, 0, len(prices))
	for name := range prices {
		names = append(names, name)
	}
	sort.Strings(names)
	table := make(map[string]int64, len(prices))
	for _, name := range names {
		p := prices[name]
		if p < 0 || p > MaxPrice {
			return nil, &Error{Op: "new", Reason: ReasonInvalidPrice,
				detail: "plan " + name + " price out of range [0,1e12]: " + itoa64(p)}
		}
		table[name] = p
	}
	if _, ok := table[initial]; !ok {
		return nil, &Error{Op: "new", Reason: ReasonUnknownPlan,
			detail: "unknown initial plan: " + initial}
	}

	e := &Engine{
		days:    D,
		prices:  table,
		period:  1,
		current: initial,
		lastDay: -1,
	}
	e.prepay()
	return e, nil
}

// Change moves the subscription from the current plan to plan on day. It
// emits a refund line followed by a charge line, or returns *Error without
// mutating any state when rejected.
func (e *Engine) Change(day int, plan string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Validation order is part of the contract: only the first violation is
	// reported, and a rejected call mutates nothing.
	price, ok := e.prices[plan]
	if !ok {
		return &Error{Op: "change", Reason: ReasonUnknownPlan,
			detail: "unknown plan: " + plan}
	}
	if plan == e.current {
		return &Error{Op: "change", Reason: ReasonSamePlan,
			detail: "plan already in effect: " + plan}
	}
	if day < 1 || day > e.days-1 {
		return &Error{Op: "change", Reason: ReasonDayOutOfRange,
			detail: "day " + itoa(day) + " not in [1," + itoa(e.days-1) + "]"}
	}
	// The repeated/ordering checks apply only after the first change.
	if e.lastDay >= 0 {
		if day == e.lastDay {
			return &Error{Op: "change", Reason: ReasonDayRepeated,
				detail: "day " + itoa(day) + " equals previous change day"}
		}
		if day < e.lastDay {
			return &Error{Op: "change", Reason: ReasonDayOutOfOrder,
				detail: "day " + itoa(day) + " before previous change day " + itoa(e.lastDay)}
		}
	}

	remain := e.days - day
	oldPrice := e.prices[e.current]

	// Refund first: never over-refund, so the fraction is rounded down.
	refund := -floorDiv(oldPrice*int64(remain), int64(e.days))
	e.appendLine(LineRefund, day, e.current, oldPrice, remain, refund)
	// Then the charge: never under-collect, so the fraction is rounded up.
	charge := ceilDiv(price*int64(remain), int64(e.days))
	e.appendLine(LineCharge, day, plan, price, remain, charge)

	e.current = plan
	e.lastDay = day
	return nil
}

// Settle closes the current period and returns a copy of all its lines
// together with their algebraic sum. It then opens a new period and prepays
// the current plan in full on its day 0.
func (e *Engine) Settle() (lines []Line, total int64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	closed := e.snapshotLines()
	sum := e.total

	// Open the next period and prepay the plan currently in effect.
	e.period++
	e.lastDay = -1
	e.lines = nil
	e.total = 0
	e.prepay()

	return closed, sum
}

// Query returns an immutable snapshot of the open period.
func (e *Engine) Query() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()

	return Snapshot{
		Period:      e.period,
		Days:        e.days,
		CurrentPlan: e.current,
		LastDay:     e.lastDay,
		Lines:       e.snapshotLines(),
		Total:       e.total,
	}
}

// prepay emits the day-0 full-price charge that opens a period.
func (e *Engine) prepay() {
	price := e.prices[e.current]
	e.appendLine(LineCharge, 0, e.current, price, e.days, price)
}

func (e *Engine) appendLine(kind LineKind, day int, plan string, price int64, remain int, amount int64) {
	e.lines = append(e.lines, Line{
		Period: e.period,
		Seq:    len(e.lines) + 1,
		Kind:   kind,
		Day:    day,
		Plan:   plan,
		Price:  price,
		Remain: remain,
		Amount: amount,
	})
	e.total += amount
}

func (e *Engine) snapshotLines() []Line {
	out := make([]Line, len(e.lines))
	copy(out, e.lines)
	return out
}

// floorDiv returns floor(a/b) for b > 0 and a >= 0.
func floorDiv(a, b int64) int64 { return a / b }

// ceilDiv returns ceil(a/b) for b > 0 and a >= 0.
func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

func itoa(n int) string { return itoa64(int64(n)) }

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
