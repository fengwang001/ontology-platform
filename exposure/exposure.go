package exposure

import "ontology/limit"

type sideState struct {
	position     int64
	pendingOpen  int64
	pendingClose int64
}

type accountState struct {
	id            string
	group         string
	sides         map[string][2]sideState
	dayFilledOpen map[string]int64
}

type groupKey struct {
	group string
	sym   string
	side  limit.Side
}

type Tracker struct {
	registry *limit.Registry
	accounts map[string]*accountState
	groups   map[groupKey]int64
}

func NewTracker(registry *limit.Registry) *Tracker {
	return &Tracker{
		registry: registry,
		accounts: make(map[string]*accountState),
		groups:   make(map[groupKey]int64),
	}
}

func (t *Tracker) Register(acct string) {
	group, _ := t.registry.Group(acct)
	t.accounts[acct] = &accountState{
		id: acct, group: group,
		sides:         make(map[string][2]sideState),
		dayFilledOpen: make(map[string]int64),
	}
}

func (t *Tracker) SetHedge(acct, sym string, side limit.Side, hedge int64) {
	account := t.account(acct)
	index := sideIndex(side)
	t.removeContribution(account, sym, index)
	_ = t.registry.SetHedge(acct, sym, side, hedge)
	t.addContribution(account, sym, index)
}

func (t *Tracker) SubmitOpen(acct, sym string, side limit.Side, qty int64) {
	account := t.account(acct)
	index := sideIndex(side)
	t.removeContribution(account, sym, index)
	values := account.sides[sym]
	values[index].pendingOpen += qty
	account.sides[sym] = values
	t.addContribution(account, sym, index)
}

func (t *Tracker) SubmitClose(acct, sym string, side limit.Side, qty int64) {
	account := t.account(acct)
	index := sideIndex(side)
	values := account.sides[sym]
	values[index].pendingClose += qty
	account.sides[sym] = values
}

func (t *Tracker) FillOpen(acct, sym string, side limit.Side, qty int64) {
	account := t.account(acct)
	index := sideIndex(side)
	t.removeContribution(account, sym, index)
	values := account.sides[sym]
	values[index].pendingOpen -= qty
	values[index].position += qty
	account.dayFilledOpen[sym] += qty
	account.sides[sym] = values
	t.addContribution(account, sym, index)
}

func (t *Tracker) FillClose(acct, sym string, side limit.Side, qty int64) {
	account := t.account(acct)
	index := sideIndex(side)
	t.removeContribution(account, sym, index)
	values := account.sides[sym]
	values[index].position -= qty
	values[index].pendingClose -= qty
	account.sides[sym] = values
	t.addContribution(account, sym, index)
}

func (t *Tracker) ReleaseOpen(acct, sym string, side limit.Side, qty int64) {
	account := t.account(acct)
	index := sideIndex(side)
	t.removeContribution(account, sym, index)
	values := account.sides[sym]
	values[index].pendingOpen -= qty
	account.sides[sym] = values
	t.addContribution(account, sym, index)
}

func (t *Tracker) ReleaseClose(acct, sym string, side limit.Side, qty int64) {
	account := t.account(acct)
	index := sideIndex(side)
	values := account.sides[sym]
	values[index].pendingClose -= qty
	account.sides[sym] = values
}

func (t *Tracker) ResetDay() {
	for _, account := range t.accounts {
		account.dayFilledOpen = make(map[string]int64)
	}
}

func (t *Tracker) Exposure(acct, sym string, side limit.Side) int64 {
	state := t.account(acct).sides[sym][sideIndex(side)]
	return state.position + state.pendingOpen
}

func (t *Tracker) Position(acct, sym string, side limit.Side) int64 {
	return t.account(acct).sides[sym][sideIndex(side)].position
}

func (t *Tracker) PendingOpen(acct, sym string, side limit.Side) int64 {
	return t.account(acct).sides[sym][sideIndex(side)].pendingOpen
}

func (t *Tracker) PendingClose(acct, sym string, side limit.Side) int64 {
	return t.account(acct).sides[sym][sideIndex(side)].pendingClose
}

func (t *Tracker) DayFilledOpen(acct, sym string) int64 {
	return t.account(acct).dayFilledOpen[sym]
}

func (t *Tracker) CloseAvailable(acct, sym string, side limit.Side) int64 {
	state := t.account(acct).sides[sym][sideIndex(side)]
	return state.position - state.pendingClose
}

func (t *Tracker) DayOpen(acct, sym string) int64 {
	account := t.account(acct)
	values := account.sides[sym]
	return account.dayFilledOpen[sym] + values[0].pendingOpen + values[1].pendingOpen
}

func (t *Tracker) GroupExposure(group, sym string, side limit.Side) int64 {
	return t.groups[groupKey{group: group, sym: sym, side: side}]
}

func (t *Tracker) account(acct string) *accountState {
	return t.accounts[acct]
}

func (t *Tracker) removeContribution(account *accountState, sym string, index int) {
	key := groupKey{group: account.group, sym: sym, side: sideOf(index)}
	t.groups[key] -= t.contribution(account, sym, index)
}

func (t *Tracker) addContribution(account *accountState, sym string, index int) {
	key := groupKey{group: account.group, sym: sym, side: sideOf(index)}
	t.groups[key] += t.contribution(account, sym, index)
}

func (t *Tracker) contribution(account *accountState, sym string, index int) int64 {
	state := account.sides[sym][index]
	exposure := state.position + state.pendingOpen
	hedge := t.registry.Hedge(account.id, sym, sideOf(index))
	if exposure > hedge {
		return exposure - hedge
	}
	return 0
}

func sideOf(index int) limit.Side {
	if index == 1 {
		return limit.Short
	}
	return limit.Long
}

func sideIndex(side limit.Side) int {
	if side == limit.Short {
		return 1
	}
	return 0
}
