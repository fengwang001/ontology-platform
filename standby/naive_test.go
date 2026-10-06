package standby

// naive.go（仅测试用）：按题意独立写成的朴素对照模型。
// 用切片 + 全量排序实现，不依赖 trie、事务等生产代码的数据结构选择，
// 但严格遵守同一条文：拒绝次序、过期时序、全有/全无、释放来源、六态。

import (
	"sort"
)

type nEntry struct {
	id         EntryID
	passenger  string
	party      int
	prio       Priority
	registered int64
	state      EntryState
	deadline   int64
}

type nFlight struct {
	capacity   int
	confirmed  int
	pendingN   int
	queueLimit int
	delay      int64
	canceled   bool
	entries    map[EntryID]*nEntry
}

type naiveSystem struct {
	now     int64
	nextID  EntryID
	flights map[flightKey]*nFlight
}

func newNaive() *naiveSystem {
	return &naiveSystem{flights: map[flightKey]*nFlight{}}
}

func (n *naiveSystem) free(f *nFlight) int { return f.capacity - f.confirmed - f.pendingN }

func (n *naiveSystem) settle(f *nFlight, now int64) {
	for {
		var due []*nEntry
		var dl int64
		found := false
		for _, e := range f.entries {
			if e.state == StatePending && (!found || e.deadline < dl) {
				dl = e.deadline
				found = true
			}
		}
		if !found || dl > now {
			return
		}
		for _, e := range f.entries {
			if e.state == StatePending && e.deadline == dl {
				due = append(due, e)
			}
		}
		for _, e := range due {
			e.state = StateExpired
			f.pendingN -= e.party
		}
		n.fulfill(f, dl)
	}
}

func (n *naiveSystem) fulfill(f *nFlight, at int64) {
	free := n.free(f)
	if free <= 0 {
		return
	}
	var waiting []*nEntry
	for _, e := range f.entries {
		if e.state == StateWaiting && e.registered <= at {
			waiting = append(waiting, e)
		}
	}
	sort.Slice(waiting, func(i, j int) bool {
		if waiting[i].prio != waiting[j].prio {
			return waiting[i].prio > waiting[j].prio
		}
		if waiting[i].registered != waiting[j].registered {
			return waiting[i].registered < waiting[j].registered
		}
		return waiting[i].id < waiting[j].id
	})
	for _, e := range waiting {
		if free == 0 {
			break
		}
		if e.party <= free {
			free -= e.party
			e.state = StatePending
			e.deadline = at + f.delay
			f.pendingN += e.party
		}
	}
}

func (n *naiveSystem) create(name, cabin string, capacity, queueLimit int, delay, now int64) error {
	if name == "" || cabin == "" || capacity <= 0 || queueLimit < 0 || delay < 0 || now < 0 {
		return ErrInvalid
	}
	if now < n.now {
		return ErrClockRewind
	}
	key := flightKeyOf(name, cabin)
	if _, ok := n.flights[key]; ok {
		return ErrInvalid
	}
	n.flights[key] = &nFlight{capacity: capacity, queueLimit: queueLimit, delay: delay,
		entries: map[EntryID]*nEntry{}}
	n.now = now
	return nil
}

func (n *naiveSystem) get(name, cabin string) *nFlight {
	return n.flights[flightKeyOf(name, cabin)]
}

func (n *naiveSystem) register(name, cabin, passenger string, party int, prio Priority, now int64) (EntryID, error) {
	if name == "" || cabin == "" || passenger == "" || party < 1 || party > 9 || !prio.Valid() || now < 0 {
		return 0, ErrInvalid
	}
	if now < n.now {
		return 0, ErrClockRewind
	}
	f := n.get(name, cabin)
	if f == nil {
		return 0, ErrFlightNotFound
	}
	if f.canceled {
		return 0, ErrFlightCanceled
	}
	n.settle(f, now)
	for _, e := range f.entries {
		if e.passenger == passenger && (e.state == StateWaiting || e.state == StatePending) {
			return 0, ErrDuplicate
		}
	}
	wn := 0
	for _, e := range f.entries {
		if e.state == StateWaiting {
			wn++
		}
	}
	if wn >= f.queueLimit {
		return 0, ErrQueueFull
	}
	n.nextID++
	e := &nEntry{id: n.nextID, passenger: passenger, party: party, prio: prio,
		registered: now, state: StateWaiting}
	f.entries[e.id] = e
	n.fulfill(f, now)
	n.now = now
	return e.id, nil
}

// prepareRegister 供差分测试使用：先做参数之外的判定（朴素侧内部结算在 snapshot 式回滚保护下）。
// 返回 (nil, err) 表示拒绝（状态不变）；返回 (commit, nil) 表示可提交，
// commit 接受与真实系统相同的 id 并完成建条目/兑现/推进时钟。
func (n *naiveSystem) prepareRegister(name, cabin, passenger string, party int, prio Priority, now int64) (func(EntryID), error) {
	if name == "" || cabin == "" || passenger == "" || party < 1 || party > 9 || !prio.Valid() || now < 0 {
		return nil, ErrInvalid
	}
	if now < n.now {
		return nil, ErrClockRewind
	}
	f := n.get(name, cabin)
	if f == nil {
		return nil, ErrFlightNotFound
	}
	if f.canceled {
		return nil, ErrFlightCanceled
	}
	saved := n.serializeFlight(f)
	savedNow := n.now
	n.settle(f, now)
	for _, e := range f.entries {
		if e.passenger == passenger && (e.state == StateWaiting || e.state == StatePending) {
			n.restoreFlight(f, saved)
			n.now = savedNow
			return nil, ErrDuplicate
		}
	}
	wn := 0
	for _, e := range f.entries {
		if e.state == StateWaiting {
			wn++
		}
	}
	if wn >= f.queueLimit {
		n.restoreFlight(f, saved)
		n.now = savedNow
		return nil, ErrQueueFull
	}
	return func(id EntryID) {
		e := &nEntry{id: id, passenger: passenger, party: party, prio: prio,
			registered: now, state: StateWaiting}
		f.entries[id] = e
		n.fulfill(f, now)
		n.now = now
	}, nil
}

func (n *naiveSystem) lookup(id EntryID) (*nEntry, *nFlight) {
	for _, f := range n.flights {
		if e, ok := f.entries[id]; ok {
			return e, f
		}
	}
	return nil, nil
}

func (n *naiveSystem) entryOp(id EntryID, now int64, kind string) error {
	if id == 0 || now < 0 {
		return ErrInvalid
	}
	if now < n.now {
		return ErrClockRewind
	}
	e, f := n.lookup(id)
	if e == nil {
		return ErrEntryNotFound
	}
	if f.canceled {
		return ErrFlightCanceled
	}
	// 结算可能改变条目状态；若随后因状态不符拒绝，必须完全撤销（含链式兑现）。
	saved := n.serializeFlight(f)
	savedNow := n.now
	reject := func(err error) error {
		n.restoreFlight(f, saved)
		n.now = savedNow
		return err
	}
	switch kind {
	case "withdraw":
		if e.state != StateWaiting && e.state != StatePending {
			return ErrEntryState
		}
		n.settle(f, now)
		if e.state != StateWaiting && e.state != StatePending {
			return reject(ErrEntryState)
		}
		if e.state == StateWaiting {
			e.state = StateWithdrawn
		} else {
			e.state = StateWithdrawn
			f.pendingN -= e.party
			n.fulfill(f, now)
		}
	case "confirm":
		if e.state != StatePending {
			return ErrEntryState
		}
		n.settle(f, now)
		if e.state != StatePending {
			return reject(ErrEntryState)
		}
		e.state = StateConfirmed
		f.pendingN -= e.party
		f.confirmed += e.party
	case "prio":
		if e.state != StateWaiting {
			return ErrEntryState
		}
		n.settle(f, now)
		if e.state != StateWaiting {
			return reject(ErrEntryState)
		}
	case "cancel":
		if e.state != StateConfirmed {
			return ErrEntryState
		}
		n.settle(f, now)
		if e.state != StateConfirmed {
			return reject(ErrEntryState)
		}
		e.state = StateWithdrawn
		f.confirmed -= e.party
		n.fulfill(f, now)
	}
	n.now = now
	return nil
}

func (n *naiveSystem) changePriority(id EntryID, prio Priority, now int64) error {
	if id == 0 || now < 0 || !prio.Valid() {
		return ErrInvalid
	}
	if err := n.entryOp(id, now, "prio"); err != nil {
		return err
	}
	e, _ := n.lookup(id)
	e.prio = prio
	return nil
}

func (n *naiveSystem) setCapacity(name, cabin string, capacity int, now int64) error {
	if name == "" || cabin == "" || capacity <= 0 || now < 0 {
		return ErrInvalid
	}
	if now < n.now {
		return ErrClockRewind
	}
	f := n.get(name, cabin)
	if f == nil {
		return ErrFlightNotFound
	}
	if f.canceled {
		return ErrFlightCanceled
	}
	n.settle(f, now)
	old := f.capacity
	f.capacity = capacity
	if capacity > old || n.free(f) > 0 {
		n.fulfill(f, now)
	}
	n.now = now
	return nil
}

func (n *naiveSystem) cancelFlight(name, cabin string, now int64) error {
	if name == "" || cabin == "" || now < 0 {
		return ErrInvalid
	}
	if now < n.now {
		return ErrClockRewind
	}
	f := n.get(name, cabin)
	if f == nil {
		return ErrFlightNotFound
	}
	if f.canceled {
		return ErrFlightCanceled
	}
	n.settle(f, now)
	f.canceled = true
	f.confirmed = 0
	f.pendingN = 0
	for _, e := range f.entries {
		e.state = StateVoided
	}
	n.now = now
	return nil
}

func (n *naiveSystem) snapshot(name, cabin string, now int64) (FlightSnapshot, error) {
	if name == "" || cabin == "" || now < 0 {
		return FlightSnapshot{}, ErrInvalid
	}
	if now < n.now {
		return FlightSnapshot{}, ErrClockRewind
	}
	f := n.get(name, cabin)
	if f == nil {
		return FlightSnapshot{}, ErrFlightNotFound
	}
	// 朴素模型直接在副本视图上结算：保存后恢复，等价于“查询不改状态”。
	saved := n.serializeFlight(f)
	n.settle(f, now)
	snap := n.buildSnapshot(f, name, cabin)
	n.restoreFlight(f, saved)
	return snap, nil
}

type nSaved struct {
	capacity  int
	confirmed int
	pendingN  int
	canceled  bool
	entries   []nEntry
}

func (n *naiveSystem) serializeFlight(f *nFlight) nSaved {
	s := nSaved{capacity: f.capacity, confirmed: f.confirmed, pendingN: f.pendingN, canceled: f.canceled}
	for _, e := range f.entries {
		s.entries = append(s.entries, *e)
	}
	return s
}

func (n *naiveSystem) restoreFlight(f *nFlight, s nSaved) {
	f.capacity, f.confirmed, f.pendingN, f.canceled = s.capacity, s.confirmed, s.pendingN, s.canceled
	f.entries = map[EntryID]*nEntry{}
	for i := range s.entries {
		e := s.entries[i]
		f.entries[e.id] = &e
	}
}

func (n *naiveSystem) buildSnapshot(f *nFlight, name, cabin string) FlightSnapshot {
	snap := FlightSnapshot{
		Flight: name, Cabin: cabin, Capacity: f.capacity, Confirmed: f.confirmed,
		Pending: f.pendingN, Canceled: f.canceled, QueueLimit: f.queueLimit,
		ConfirmationDelay: f.delay, Free: n.free(f),
	}
	for _, e := range f.entries {
		if e.state == StateWaiting {
			snap.WaitingCount++
		}
		snap.Entries = append(snap.Entries, EntryInfo{
			ID: e.id, Passenger: e.passenger, Party: e.party, Priority: e.prio,
			Registered: e.registered, State: e.state, Deadline: e.deadline,
		})
	}
	sort.Slice(snap.Entries, func(i, j int) bool { return snap.Entries[i].ID < snap.Entries[j].ID })
	if snap.Entries == nil {
		snap.Entries = []EntryInfo{}
	}
	return snap
}
