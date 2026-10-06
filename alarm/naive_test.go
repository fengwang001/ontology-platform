package alarm

import "sort"

type naivePoint struct {
	priority       Priority
	conditions     []string
	state          State
	lastActivation int64
	disabled       bool
	until          int64
	reason         string
	unack          bool
	activations    []int64
}

type naiveModel struct {
	cfg         Config
	points      map[string]*naivePoint
	conditions  map[string]bool
	clock       int64
	shown       map[string]struct{}
	appearances []int64
	expiries    map[string]int64
}

func newNaiveModel(cfg Config, configs []PointConfig) *naiveModel {
	m := &naiveModel{
		cfg:        cfg,
		points:     make(map[string]*naivePoint),
		conditions: make(map[string]bool),
		shown:      make(map[string]struct{}),
		expiries:   make(map[string]int64),
	}
	for _, c := range configs {
		m.points[c.ID] = &naivePoint{priority: c.Priority, conditions: append([]string(nil), c.Conditions...)}
	}
	return m
}

type naiveEvent struct {
	name      string
	at        int64
	point     string
	role      Role
	duration  int64
	reason    string
	ticket    string
	condition string
	active    bool
	invalid   bool
}

func (m *naiveModel) inhibited(p *naivePoint) bool {
	for _, name := range p.conditions {
		if m.conditions[name] {
			return true
		}
	}
	return false
}

func (m *naiveModel) hidden(at int64, p *naivePoint) bool {
	return p.until > at || p.disabled || m.inhibited(p)
}

func (m *naiveModel) apply(e naiveEvent) (bool, ErrorKind, bool) {
	if e.invalid || e.at < 0 || e.point == "" && e.name != "condition" {
		return false, InvalidArgument, false
	}
	if e.name == "condition" {
		if e.condition == "" {
			return false, InvalidArgument, false
		}
		if e.at < m.clock {
			return false, ClockRewound, false
		}
		known := false
		for _, p := range m.points {
			for _, name := range p.conditions {
				if name == e.condition {
					known = true
				}
			}
		}
		if !known {
			return false, PointNotFound, false
		}
		m.clock = e.at
		m.conditions[e.condition] = e.active
		m.expireAndShow(e.at)
		return false, 255, false
	}

	if e.at < m.clock {
		return false, ClockRewound, false
	}
	p := m.points[e.point]
	if p == nil {
		return false, PointNotFound, false
	}
	if e.role != Operator && e.role != Engineer {
		return false, InvalidArgument, false
	}
	engineerOnly := e.name == "disable" || e.name == "enable"
	if engineerOnly && e.role != Engineer {
		return false, PermissionDenied, false
	}

	switch e.name {
	case "trigger":
		if p.state == ActiveUnacknowledged || p.state == ActiveAcknowledged {
			m.expireTo(e.at)
			m.clock = e.at
			m.expireAndShow(e.at)
			return true, 255, false
		}
		if p.disabled {
			m.expireTo(e.at)
			m.clock = e.at
			return true, 255, false
		}
		m.expireTo(e.at)
		p.state = ActiveUnacknowledged
		p.lastActivation = e.at
		p.activations = append(p.activations, e.at)
		m.clock = e.at
		if m.hidden(e.at, p) {
			p.unack = true
		} else if p.priority != Emergency && m.chatterHit(p) {
			p.until = e.at + m.cfg.ChatterDuration
			p.reason = ChatterReason
			p.unack = true
			m.expiries[e.point] = p.until
		}
	case "return":
		if !p.disabled && p.state != ActiveUnacknowledged && p.state != ActiveAcknowledged {
			return false, StateNotAllowed, false
		}
		m.expireTo(e.at)
		if p.disabled {
			m.clock = e.at
			return true, 255, false
		}
		if p.state == ActiveUnacknowledged {
			p.state = ReturnedUnacknowledged
		} else {
			p.state = Normal
			if p.unack {
				p.state = ReturnedUnacknowledged
			}
		}
		m.clock = e.at
	case "acknowledge":
		if p.state != ActiveUnacknowledged && p.state != ReturnedUnacknowledged {
			return false, StateNotAllowed, false
		}
		m.expireTo(e.at)
		wasReturnedUnacknowledged := p.state == ReturnedUnacknowledged
		if !wasReturnedUnacknowledged {
			p.state = ActiveAcknowledged
		} else {
			p.state = Normal
			p.unack = false
		}
		m.clock = e.at
	case "suppress":
		if e.duration <= 0 || e.reason == "" {
			return false, InvalidArgument, false
		}
		if p.priority == Emergency || p.disabled || p.until > e.at {
			return false, StateNotAllowed, false
		}
		limit := m.cfg.HighManualDuration
		if p.priority == Low {
			limit = m.cfg.LowManualDuration
		}
		if e.duration > limit {
			return false, DurationLimitExceeded, false
		}
		m.discardExpiredForPoint(e.point, e.at)
		if p.priority == Emergency || p.disabled || p.until > e.at {
			return false, StateNotAllowed, false
		}
		delete(m.expiries, e.point)
		p.until = e.at + e.duration
		p.reason = e.reason
		m.expiries[e.point] = p.until
		m.clock = e.at
	case "release":
		if p.until <= e.at {
			return false, StateNotAllowed, false
		}
		p.until = 0
		p.reason = ""
		delete(m.expiries, e.point)
		m.clock = e.at
	case "disable":
		if e.ticket == "" {
			return false, InvalidArgument, false
		}
		if p.disabled {
			return false, StateNotAllowed, false
		}
		m.expireTo(e.at)
		p.disabled = true
		m.clock = e.at
	case "enable":
		if e.ticket == "" {
			return false, InvalidArgument, false
		}
		if !p.disabled {
			return false, StateNotAllowed, false
		}
		m.expireTo(e.at)
		p.disabled = false
		p.state = Normal
		p.lastActivation = 0
		p.unack = false
		p.until = 0
		p.reason = ""
		delete(m.expiries, e.point)
		m.clock = e.at
	default:
		return false, InvalidArgument, false
	}
	m.expireAndShow(e.at)
	return false, 255, false
}

func (m *naiveModel) discardExpiredForPoint(pointID string, at int64) {
	p := m.points[pointID]
	if until, exists := m.expiries[pointID]; exists && until <= at && until == p.until {
		p.until = 0
		p.reason = ""
		delete(m.expiries, pointID)
	}
}

func (m *naiveModel) chatterHit(p *naivePoint) bool {
	current := p.activations[len(p.activations)-1]
	count := 0
	for _, at := range p.activations {
		if at > current-m.cfg.ChatterWindow && at <= current {
			count++
		}
	}
	return count >= m.cfg.ChatterCount
}

func (m *naiveModel) expireTo(at int64) {
	for {
		nextID := ""
		var nextAt int64
		for id, until := range m.expiries {
			if until <= at && until == m.points[id].until && (nextID == "" || until < nextAt || until == nextAt && id < nextID) {
				nextID = id
				nextAt = until
			}
		}
		if nextID == "" {
			break
		}
		m.points[nextID].until = 0
		m.points[nextID].reason = ""
		delete(m.expiries, nextID)
		m.expireAndShow(nextAt)
	}
	m.expireAndShow(at)
}

func (m *naiveModel) expireAndShow(at int64) {
	for id, p := range m.points {
		was := false
		if _, ok := m.shown[id]; ok {
			was = true
		}
		now := p.state != Normal && !m.hidden(at, p)
		if now && !was {
			m.shown[id] = struct{}{}
			m.appearances = append(m.appearances, at)
		}
		if !now {
			delete(m.shown, id)
		}
	}
}

func (m *naiveModel) list() []ActiveAlarm {
	out := make([]ActiveAlarm, 0, len(m.shown))
	for id := range m.shown {
		p := m.points[id]
		state := p.state
		if p.unack && (state == ActiveAcknowledged || state == ReturnedUnacknowledged) {
			state = ActiveUnacknowledged
		}
		out = append(out, ActiveAlarm{PointID: id, Priority: p.priority, State: state, LastActivation: p.lastActivation})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		ui := out[i].State == ActiveUnacknowledged || out[i].State == ReturnedUnacknowledged
		uj := out[j].State == ActiveUnacknowledged || out[j].State == ReturnedUnacknowledged
		if ui != uj {
			return ui
		}
		if out[i].LastActivation != out[j].LastActivation {
			return out[i].LastActivation < out[j].LastActivation
		}
		return out[i].PointID < out[j].PointID
	})
	return out
}

func (m *naiveModel) query(at int64) {
	m.clock = at
	m.expireTo(at)
}

func (m *naiveModel) rate(at int64, duration int64) int {
	count := 0
	for _, shownAt := range m.appearances {
		if shownAt > at-duration && shownAt <= at {
			count++
		}
	}
	return count
}
