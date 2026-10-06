package vrrp_test

import (
	"errors"

	"ontology/vrrp"
)

type operationKind int

const (
	opStart operationKind = iota
	opStop
	opReceive
	opAdvance
	opTake
	opSetPriority
	opSetPreempt
)

type step struct {
	kind     operationKind
	now      uint64
	priority int
	preempt  bool
	sender   string
	interval uint64
}

type expected struct {
	err      error
	role     vrrp.Role
	priority int
	preempt  bool
	adverts  []vrrp.Advertisement
	reason   string
}

type naiveModel struct {
	config   vrrp.Config
	role     vrrp.Role
	priority int
	preempt  bool

	hasClock bool
	lastTime uint64

	monitor      bool
	monitorUntil uint64

	downWait  bool
	downUntil uint64

	masterDue bool
	dueAt     uint64
	pending   bool
}

func newNaiveModel(config vrrp.Config) *naiveModel {
	return &naiveModel{
		config:   config,
		role:     vrrp.RoleInitialize,
		priority: config.Priority,
		preempt:  config.Preempt,
	}
}

func validConfig(config vrrp.Config) bool {
	return config.ID != "" &&
		config.Priority >= 1 && config.Priority <= 255 &&
		config.AdvertIntervalMS >= 1 && config.AdvertIntervalMS <= 1_000_000
}

func (m *naiveModel) advert(step step) vrrp.Advertisement {
	return vrrp.Advertisement{
		SenderID:         step.sender,
		Priority:         step.priority,
		AdvertIntervalMS: step.interval,
	}
}

func (m *naiveModel) ownAdvert() vrrp.Advertisement {
	return vrrp.Advertisement{
		SenderID:         m.config.ID,
		Priority:         m.priority,
		AdvertIntervalMS: m.config.AdvertIntervalMS,
	}
}

func output(adverts ...vrrp.Advertisement) []vrrp.Advertisement {
	if len(adverts) == 0 {
		return nil
	}
	return adverts
}

func (m *naiveModel) result(reason string, adverts ...vrrp.Advertisement) expected {
	return expected{
		err:      nil,
		role:     m.role,
		priority: m.priority,
		preempt:  m.preempt,
		adverts:  output(adverts...),
		reason:   reason,
	}
}

func (m *naiveModel) failure(err error, reason string) expected {
	return expected{
		err:      err,
		role:     m.role,
		priority: m.priority,
		preempt:  m.preempt,
		adverts:  nil,
		reason:   reason,
	}
}

func (m *naiveModel) becomeMaster(now uint64, immediate bool, reason string) expected {
	m.role = vrrp.RoleMaster
	m.monitor = false
	m.downWait = false
	m.masterDue = true
	m.dueAt = now + m.config.AdvertIntervalMS

	if immediate {
		m.pending = false
		return m.result(reason, m.ownAdvert())
	}

	m.pending = true
	return m.result(reason)
}

func (m *naiveModel) becomeBackup(now, senderInterval uint64) {
	m.role = vrrp.RoleBackup
	m.monitor = true
	m.monitorUntil = now + 3*senderInterval
	m.downWait = false
	m.masterDue = false
	m.pending = false
}

func (m *naiveModel) becomeInitialized() {
	m.role = vrrp.RoleInitialize
	m.monitor = false
	m.downWait = false
	m.masterDue = false
	m.pending = false
}

func (m *naiveModel) invalidAdvert(step step) bool {
	return step.sender == "" ||
		step.priority < 0 || step.priority > 255 ||
		step.interval < 1 || step.interval > 1_000_000
}

func (m *naiveModel) timeRegression(now uint64) bool {
	return m.hasClock && now < m.lastTime
}

func (m *naiveModel) acceptTime(now uint64) {
	m.hasClock = true
	m.lastTime = now
}

func (m *naiveModel) handle(current step) expected {
	switch current.kind {
	case opReceive:
		if m.invalidAdvert(current) {
			return m.failure(vrrp.ErrInvalidArgument, "invalid advertisement parameter")
		}
		if m.timeRegression(current.now) {
			return m.failure(vrrp.ErrClockRegression, "clock moved backwards")
		}
		if m.role == vrrp.RoleInitialize {
			return m.failure(vrrp.ErrNotStarted, "advertisement ignored before start")
		}

		m.acceptTime(current.now)
		incoming := m.advert(current)

		if m.role == vrrp.RoleBackup {
			if incoming.Priority == 255 && m.priority == 255 {
				return m.failure(vrrp.ErrIdentityConflict, "duplicate address owner")
			}
			if incoming.Priority == m.priority && incoming.SenderID == m.config.ID {
				return m.failure(vrrp.ErrIdentityConflict, "duplicate device identity")
			}

			if incoming.Priority == 0 {
				m.downWait = true
				m.downUntil = current.now + incoming.AdvertIntervalMS/4
				return m.result("priority-zero advertisement starts shutdown wait")
			}

			higherPriority := incoming.Priority > m.priority
			samePriorityGreaterID := incoming.Priority == m.priority && incoming.SenderID > m.config.ID
			lowerPriorityWithNoPreemption := incoming.Priority < m.priority && !m.preempt
			adopt := higherPriority || samePriorityGreaterID || lowerPriorityWithNoPreemption
			if adopt {
				m.monitor = true
				m.monitorUntil = current.now + 3*incoming.AdvertIntervalMS
				m.downWait = false
				return m.result("advertisement adopted; monitor reset")
			}
			return m.result("lower-priority advertisement ignored with preemption enabled")
		}

		if m.priority == 255 {
			if incoming.Priority == 255 {
				return m.failure(vrrp.ErrIdentityConflict, "duplicate address owner")
			}
			return m.result("address owner ignores non-owner advertisement")
		}

		if incoming.Priority == 0 {
			m.masterDue = true
			m.dueAt = current.now + m.config.AdvertIntervalMS
			m.pending = false
			return m.result("priority-zero probe answered immediately", m.ownAdvert())
		}

		if incoming.Priority == m.priority && incoming.SenderID == m.config.ID {
			return m.failure(vrrp.ErrIdentityConflict, "duplicate device identity")
		}

		if incoming.Priority > m.priority ||
			(incoming.Priority == m.priority && incoming.SenderID > m.config.ID) {
			m.becomeBackup(current.now, incoming.AdvertIntervalMS)
			return m.result("master demoted by superior advertisement")
		}
		return m.result("inferior master advertisement ignored")

	case opAdvance:
		if m.timeRegression(current.now) {
			return m.failure(vrrp.ErrClockRegression, "clock moved backwards")
		}
		m.acceptTime(current.now)

		if m.role != vrrp.RoleBackup {
			return m.result("time advancement does not change role")
		}

		if m.downWait && current.now >= m.downUntil {
			return m.becomeMaster(current.now, false, "shutdown wait expired; queued advertisement")
		}
		if m.monitor && current.now >= m.monitorUntil {
			return m.becomeMaster(current.now, false, "master timeout expired; queued advertisement")
		}
		return m.result("backup timers remain active")

	case opStart:
		if m.timeRegression(current.now) {
			return m.failure(vrrp.ErrClockRegression, "clock moved backwards")
		}
		m.acceptTime(current.now)
		if m.role != vrrp.RoleInitialize {
			return m.result("start ignored while already running")
		}
		if m.priority == 255 {
			return m.becomeMaster(current.now, true, "address owner starts as master")
		}
		m.becomeBackup(current.now, m.config.AdvertIntervalMS)
		return m.result("non-owner starts as backup")

	case opStop:
		if m.timeRegression(current.now) {
			return m.failure(vrrp.ErrClockRegression, "clock moved backwards")
		}
		m.acceptTime(current.now)
		if m.role == vrrp.RoleMaster {
			m.becomeInitialized()
			return m.result("master stops and sends priority-zero advertisement", vrrp.Advertisement{
				SenderID:         m.config.ID,
				Priority:         0,
				AdvertIntervalMS: m.config.AdvertIntervalMS,
			})
		}
		m.becomeInitialized()
		return m.result("non-master returns to initialize")

	case opTake:
		if m.timeRegression(current.now) {
			return m.failure(vrrp.ErrClockRegression, "clock moved backwards")
		}
		m.acceptTime(current.now)
		if m.role != vrrp.RoleMaster {
			return m.result("take while not master produces nothing")
		}
		if m.pending {
			m.pending = false
			m.masterDue = true
			m.dueAt = current.now + m.config.AdvertIntervalMS
			return m.result("queued promotion advertisement taken", m.ownAdvert())
		}
		if m.masterDue && current.now >= m.dueAt {
			m.dueAt = current.now + m.config.AdvertIntervalMS
			return m.result("periodic advertisement due", m.ownAdvert())
		}
		return m.result("no advertisement due")

	case opSetPriority:
		if current.priority < 1 || current.priority > 255 ||
			(current.priority == 255) != (m.priority == 255) {
			return m.failure(vrrp.ErrInvalidArgument, "invalid priority transition")
		}
		if m.timeRegression(current.now) {
			return m.failure(vrrp.ErrClockRegression, "clock moved backwards")
		}
		m.acceptTime(current.now)
		m.priority = current.priority
		return m.result("priority updated for future comparisons")

	case opSetPreempt:
		if m.timeRegression(current.now) {
			return m.failure(vrrp.ErrClockRegression, "clock moved backwards")
		}
		m.acceptTime(current.now)
		m.preempt = current.preempt
		return m.result("preemption mode updated for future comparisons")
	}

	return m.failure(vrrp.ErrInvalidArgument, "unknown operation")
}

func sameError(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}
