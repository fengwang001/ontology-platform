package inline

// Account holds the O(1) budget bookkeeping for one root function.
//
// It stores only data about its own root: neither fits nor checks touch other
// functions, so the cost of a decision is independent of the total number of
// functions in the program.
type Account struct {
	initial int // original body size
	current int // size at the moment of the current check
	cfg     Config
}

func newAccount(initial int, cfg Config) *Account {
	return &Account{initial: initial, current: initial, cfg: cfg}
}

// delta is the size change caused by copying calleeSize.
func (a *Account) delta(calleeSize int) int {
	return calleeSize - a.cfg.CallOverhead
}

// growthCap is the absolute maximum current size allowed by the growth budget.
// Equality is allowed: current <= cap.
func (a *Account) growthCap() int {
	num := int64(a.initial) * int64(a.cfg.GrowthMultiple)
	return a.initial + int(num/int64(a.cfg.GrowthMultipleDen))
}

// allows reports whether inlining now fits both hard constraints, measured
// against the current size, not the initial one.
func (a *Account) allows(calleeSize int) bool {
	next := a.current + a.delta(calleeSize)
	if next > a.cfg.GlobalSizeLimit {
		return false
	}
	return next <= a.growthCap()
}

// apply records an accepted inlining (including an over-budget always-inline
// one): the overrun permanently raises the baseline for later sites.
func (a *Account) apply(calleeSize int) {
	a.current += a.delta(calleeSize)
}

// Current is the function size at the current moment.
func (a *Account) Current() int { return a.current }
