package presence

import "sort"

// naiveModel is a deliberately simple, independently written reference: it
// keeps no heaps, performs no lazy aggregation, and instead records every
// accepted mutation as an event. Visibility is recomputed by linear replay
// up to the observed instant. It mirrors the required precedence rules.
type naiveModel struct {
	T         int64
	users     map[string]int64
	devices   map[string]map[string]struct{}
	subs      map[string]map[string]int64
	subArr    map[string]map[string]int
	opCount   int
	delivered map[string]map[string]int
	events    []naiveEvent
}

type naiveKind int

const (
	nvReport naiveKind = iota
	nvOffline
	nvInvisible
	nvBlock
	nvUnblock
)

type naiveEvent struct {
	kind    naiveKind
	user    string
	device  string
	status  Status
	on      bool
	who     string
	time    int64
	arrival int
}

func newNaive(T int64) *naiveModel {
	return &naiveModel{
		T:         T,
		users:     map[string]int64{},
		devices:   map[string]map[string]struct{}{},
		subs:      map[string]map[string]int64{},
		subArr:    map[string]map[string]int{},
		delivered: map[string]map[string]int{},
	}
}

func (m *naiveModel) exists(u string) bool { _, ok := m.users[u]; return ok }

// deviceAlive reports whether device b of a has a lease still live at now,
// matching lazy expiry: expiry exactly at now is already gone.
func (m *naiveModel) deviceAlive(a, b string, now int64) bool {
	var expiry int64
	found := false
	for _, e := range m.events {
		if e.user != a || e.time > now {
			continue
		}
		if e.kind == nvReport && e.device == b {
			expiry = e.time + m.T
			found = true
		}
		if e.kind == nvOffline && e.device == b {
			found = false
		}
	}
	if !found {
		return false
	}
	return expiry > now
}

// apply performs an operation on the model. It returns the same error class
// the service must return (nil for accepted), allowing direct comparison.
func (m *naiveModel) apply(kind string, a, b string, st Status, on bool, now int64) error {
	m.opCount++
	bad := func() error { return ErrInvalidArgument }
	switch kind {
	case "report":
		if a == "" || b == "" || st < Away || st > Online || now < 0 || now > 1e12 {
			return bad()
		}
		if m.exists(a) && now < m.users[a] {
			return ErrClockBackwards
		}
		if !m.exists(a) {
			m.users[a] = 0
			m.devices[a] = map[string]struct{}{}
		}
		alive := 0
		for dev := range m.devices[a] {
			if m.deviceAlive(a, dev, now) {
				alive++
			}
		}
		if _, ok := m.devices[a][b]; !ok && alive >= 8 {
			return ErrTooManyDevices
		}
		m.devices[a][b] = struct{}{}
		m.users[a] = now
		m.events = append(m.events, naiveEvent{
			kind: nvReport, user: a, device: b, status: st,
			time: now, arrival: m.opCount,
		})
		return nil
	case "offline":
		if a == "" || b == "" || now < 0 || now > 1e12 {
			return bad()
		}
		if !m.exists(a) {
			return ErrNotFound
		}
		if now < m.users[a] {
			return ErrClockBackwards
		}
		if !m.deviceAlive(a, b, now) {
			return ErrNotFound
		}
		delete(m.devices[a], b)
		m.users[a] = now
		m.events = append(m.events, naiveEvent{
			kind: nvOffline, user: a, device: b,
			time: now, arrival: m.opCount,
		})
		return nil
	case "invisible":
		if a == "" || now < 0 || now > 1e12 {
			return bad()
		}
		if !m.exists(a) {
			return ErrNotFound
		}
		if now < m.users[a] {
			return ErrClockBackwards
		}
		m.users[a] = now
		m.events = append(m.events, naiveEvent{
			kind: nvInvisible, user: a, on: on,
			time: now, arrival: m.opCount,
		})
		return nil
	case "block", "unblock":
		if a == "" || b == "" || now < 0 || now > 1e12 {
			return bad()
		}
		if a == b {
			return ErrCannotBlockSelf
		}
		if !m.exists(a) || !m.exists(b) {
			return ErrNotFound
		}
		if now < m.users[a] || now < m.users[b] {
			return ErrClockBackwards
		}
		k := nvBlock
		m.users[a] = now
		m.users[b] = now
		if kind == "unblock" {
			k = nvUnblock
		}
		m.events = append(m.events, naiveEvent{
			kind: k, user: a, who: b,
			time: now, arrival: m.opCount,
		})
		return nil
	case "subscribe":
		if a == "" || b == "" || now < 0 || now > 1e12 {
			return bad()
		}
		if a == b {
			return ErrCannotSubscribeSelf
		}
		if !m.exists(a) || !m.exists(b) {
			return ErrNotFound
		}
		if now < m.users[a] || now < m.users[b] {
			return ErrClockBackwards
		}
		if m.subs[a] != nil {
			if _, ok := m.subs[a][b]; ok {
				return ErrAlreadySubscribed
			}
		}
		if m.subs[a] == nil {
			m.subs[a] = map[string]int64{}
		}
		m.subs[a][b] = now
		if m.subArr[a] == nil {
			m.subArr[a] = map[string]int{}
		}
		// Arrival point of the subscribe operation itself: events with
		// arrival < this point predate the subscription.
		m.subArr[a][b] = m.opCount
		if m.delivered[a] == nil {
			m.delivered[a] = map[string]int{}
		}
		m.delivered[a][b] = 0
		m.users[a] = now
		m.users[b] = now
		return nil
	case "unsubscribe":
		if a == "" || b == "" || now < 0 || now > 1e12 {
			return bad()
		}
		if !m.exists(a) || !m.exists(b) || m.subs[a] == nil {
			return ErrNotFound
		}
		if _, ok := m.subs[a][b]; !ok {
			return ErrNotFound
		}
		if now < m.users[a] || now < m.users[b] {
			return ErrClockBackwards
		}
		delete(m.subs[a], b)
		delete(m.subArr[a], b)
		delete(m.delivered[a], b)
		m.users[a] = now
		m.users[b] = now
		return nil
	}
	return bad()
}

type nDev struct {
	status Status
	expiry int64
}

// snapshot replays all events affecting target up to now and returns the real
// aggregate, the device list and active count.
func (m *naiveModel) snapshot(target string, now int64) (Status, []nDev, int) {
	devs := map[string]nDev{}
	for _, e := range m.events {
		if e.user != target || e.time > now {
			continue
		}
		switch e.kind {
		case nvReport:
			devs[e.device] = nDev{status: e.status, expiry: e.time + m.T}
		case nvOffline:
			delete(devs, e.device)
		}
	}
	active := []nDev{}
	for _, d := range devs {
		if d.expiry <= now {
			continue // exactly expired counts as expired
		}
		active = append(active, d)
	}
	best := Offline
	for _, d := range active {
		if d.status > best {
			best = d.status
		}
	}
	return best, active, len(active)
}

func (m *naiveModel) invisibleAt(target string, now int64) bool {
	v := false
	for _, e := range m.events {
		if e.user == target && e.time <= now && e.kind == nvInvisible {
			v = e.on
		}
	}
	return v
}

func (m *naiveModel) blockedAt(owner, viewer string, now int64) bool {
	v := false
	for _, e := range m.events {
		if e.user == owner && e.who == viewer && e.time <= now &&
			(e.kind == nvBlock || e.kind == nvUnblock) {
			v = e.kind == nvBlock
		}
	}
	return v
}

func (m *naiveModel) visible(viewer, target string, now int64) Status {
	real, _, _ := m.snapshot(target, now)
	if viewer == target {
		return real
	}
	if m.invisibleAt(target, now) || m.blockedAt(target, viewer, now) {
		return Offline
	}
	return real
}

type nNote struct {
	target  string
	status  Status
	eff     int64
	arrival int
	switchE bool
}

type replayStep struct {
	eff     int64
	switchE bool
	arrival int
	kind    naiveKind
	ev      naiveEvent
}

// drain replays each subscription as a discrete event simulation: device
// leases are scheduled expiry steps; reports/offlines/invisible/block are
// switch steps. Steps sort by effective time, expiries before switches at an
// equal instant, then by originating arrival. Visibility is tracked
// incrementally, so hidden transitions are skipped but never backfilled.
func (m *naiveModel) drain(viewer string, now int64) ([]nNote, int) {
	var notes []nNote
	targets := make([]string, 0)
	if m.subs[viewer] != nil {
		for t := range m.subs[viewer] {
			targets = append(targets, t)
		}
	}
	sort.Strings(targets)
	for _, t := range targets {
		start := m.subs[viewer][t]
		if start > now {
			continue
		}
		startArr := 0
		if m.subArr[viewer] != nil {
			startArr = m.subArr[viewer][t]
		}
		type lease struct {
			expiry  int64
			arrival int
			device  string
		}
		active := map[string]lease{}
		var steps []replayStep
		invis := false
		blocked := false

		// Prime state with every event that arrived before the subscription
		// operation, regardless of its timestamp: accepted future-dated
		// operations on other users are already committed in arrival order.
		for _, e := range m.events {
			if e.user != t || e.arrival >= startArr {
				continue
			}
			switch e.kind {
			case nvReport:
				active[e.device] = lease{expiry: e.time + m.T, arrival: e.arrival, device: e.device}
			case nvOffline:
				delete(active, e.device)
			case nvInvisible:
				invis = e.on
			case nvBlock:
				if e.who == viewer {
					blocked = true
				}
			case nvUnblock:
				if e.who == viewer {
					blocked = false
				}
			}
		}
		for _, e := range m.events {
			if e.user != t || e.arrival <= startArr || e.time > now {
				continue
			}
			switch e.kind {
			case nvReport:
				active[e.device] = lease{
					expiry: e.time + m.T, arrival: e.arrival, device: e.device,
				}
				steps = append(steps, replayStep{
					eff: e.time, switchE: true, arrival: e.arrival,
					kind: nvReport, ev: e,
				})
			case nvOffline:
				delete(active, e.device)
				steps = append(steps, replayStep{
					eff: e.time, switchE: true, arrival: e.arrival,
					kind: nvOffline, ev: e,
				})
			case nvInvisible:
				steps = append(steps, replayStep{
					eff: e.time, switchE: true, arrival: e.arrival,
					kind: nvInvisible, ev: e,
				})
			case nvBlock, nvUnblock:
				if e.who == viewer {
					steps = append(steps, replayStep{
						eff: e.time, switchE: true, arrival: e.arrival,
						kind: e.kind, ev: e,
					})
				}
			}
		}
		for dev, l := range active {
			if l.expiry <= start {
				delete(active, dev)
			}
		}
		for dev, l := range active {
			if l.expiry <= now {
				steps = append(steps, replayStep{
					eff: l.expiry, arrival: l.arrival, kind: -1,
					ev: naiveEvent{user: t, device: dev},
				})
			}
		}

		// Incremental device-state simulation, primed at subscription time.
		type dstate struct {
			st     Status
			expiry int64
		}
		dst := map[string]dstate{}
		real := Offline
		recompute := func() {
			best := Offline
			for _, d := range dst {
				if d.st > best {
					best = d.st
				}
			}
			real = best
		}
		for _, e := range m.events {
			if e.user != t || e.arrival >= startArr {
				continue
			}
			if e.kind == nvReport {
				dst[e.device] = dstate{st: e.status, expiry: e.time + m.T}
			}
			if e.kind == nvOffline {
				delete(dst, e.device)
			}
		}
		for d, ds := range dst {
			if ds.expiry <= start {
				delete(dst, d)
			}
		}
		recompute()

		sort.SliceStable(steps, func(i, j int) bool {
			if steps[i].eff != steps[j].eff {
				return steps[i].eff < steps[j].eff
			}
			if steps[i].switchE != steps[j].switchE {
				return !steps[i].switchE
			}
			return steps[i].arrival < steps[j].arrival
		})
		last := real
		if viewer != t && (invis || blocked) {
			last = Offline
		}
		var per []nNote
		for _, st := range steps {
			switch st.kind {
			case -1:
				delete(dst, st.ev.device)
				recompute()
			case nvReport:
				dst[st.ev.device] = dstate{
					st: st.ev.status, expiry: st.ev.time + m.T,
				}
				recompute()
			case nvOffline:
				delete(dst, st.ev.device)
				recompute()
			}
			switch st.kind {
			case nvInvisible:
				invis = st.ev.on
			case nvBlock:
				blocked = true
			case nvUnblock:
				blocked = false
			}
			vis := real
			if viewer != t && (invis || blocked) {
				vis = Offline
			}
			if vis != last {
				per = append(per, nNote{
					target: t, status: vis, eff: st.eff,
					arrival: st.arrival, switchE: st.switchE,
				})
				last = vis
			}
		}
		cursor := 0
		if m.delivered[viewer] != nil {
			cursor = m.delivered[viewer][t]
		}
		if cursor > len(per) {
			cursor = len(per)
		}
		fresh := per[cursor:]
		if m.delivered[viewer] == nil {
			m.delivered[viewer] = map[string]int{}
		}
		m.delivered[viewer][t] = len(per)
		notes = append(notes, fresh...)
	}
	sort.SliceStable(notes, func(i, j int) bool {
		if notes[i].eff != notes[j].eff {
			return notes[i].eff < notes[j].eff
		}
		if notes[i].switchE != notes[j].switchE {
			return !notes[i].switchE
		}
		return notes[i].arrival < notes[j].arrival
	})
	dropped := 0
	if len(notes) > 1000 {
		dropped = len(notes) - 1000
		notes = notes[dropped:]
	}
	return notes, dropped
}
