package firmware

import "sync"

type ValidationError string

func (err ValidationError) Error() string {
	return string(err)
}

const (
	ErrInvalidInitialVersion ValidationError = "invalid initial version: must be at least 1"
	ErrInvalidTrialBoots     ValidationError = "invalid trial boot count: must be at least 1"
	ErrInvalidInstallVersion ValidationError = "invalid install version: version must not be zero"
	ErrVersionNotAboveFloor  ValidationError = "install rejected: version must be greater than floor"
	ErrInactiveSlotOnTrial   ValidationError = "install rejected: inactive slot is already on trial"
	ErrNoTrialToConfirm      ValidationError = "confirm rejected: last boot was not a trial boot"
)

type SlotStatus string

const (
	StatusEmpty SlotStatus = "Empty"
	StatusGood  SlotStatus = "Good"
	StatusBad   SlotStatus = "Bad"
	StatusTrial SlotStatus = "Trial"
)

type Slot struct {
	Version    int
	Status     SlotStatus
	TrialsLeft int
}

type BootResult struct {
	Slot     int
	Version  int
	Trial    bool
	Rollback bool
}

type State struct {
	Slots         [2]Slot
	ActiveSlot    int
	Floor         int
	TrialBoots    int
	LastBootTrial bool
}

type Manager struct {
	mu sync.Mutex

	slots         [2]Slot
	activeSlot    int
	floor         int
	trialBoots    int
	lastBootTrial bool
}

func NewManager(initialVersion int, trialBoots int) (*Manager, error) {
	if initialVersion < 1 {
		return nil, ErrInvalidInitialVersion
	}
	if trialBoots < 1 {
		return nil, ErrInvalidTrialBoots
	}

	return &Manager{
		slots: [2]Slot{
			{Version: initialVersion, Status: StatusGood},
			{Version: 0, Status: StatusEmpty},
		},
		activeSlot: 0,
		floor:      initialVersion,
		trialBoots: trialBoots,
	}, nil
}

func (m *Manager) Install(version int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	inactive := 1 - m.activeSlot
	if version == 0 {
		return ErrInvalidInstallVersion
	}
	if version <= m.floor {
		return ErrVersionNotAboveFloor
	}
	if m.slots[inactive].Status == StatusTrial {
		return ErrInactiveSlotOnTrial
	}

	m.slots[inactive] = Slot{
		Version:    version,
		Status:     StatusTrial,
		TrialsLeft: m.trialBoots,
	}

	return nil
}

func (m *Manager) Boot() BootResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	inactive := 1 - m.activeSlot
	if m.slots[inactive].Status == StatusTrial {
		if m.slots[inactive].TrialsLeft > 0 {
			m.slots[inactive].TrialsLeft--
			m.lastBootTrial = true
			return BootResult{
				Slot:    inactive,
				Version: m.slots[inactive].Version,
				Trial:   true,
			}
		}

		m.slots[inactive].Status = StatusBad
		m.slots[inactive].TrialsLeft = 0
		m.lastBootTrial = false
		return BootResult{
			Slot:     m.activeSlot,
			Version:  m.slots[m.activeSlot].Version,
			Trial:    false,
			Rollback: true,
		}
	}

	m.lastBootTrial = false
	return BootResult{
		Slot:    m.activeSlot,
		Version: m.slots[m.activeSlot].Version,
	}
}

func (m *Manager) Confirm() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.lastBootTrial {
		return ErrNoTrialToConfirm
	}

	trialSlot := 1 - m.activeSlot
	m.slots[trialSlot].Status = StatusGood
	m.slots[trialSlot].TrialsLeft = 0
	m.floor = m.slots[trialSlot].Version
	m.activeSlot = trialSlot
	m.lastBootTrial = false

	return nil
}

func (m *Manager) Snapshot() State {
	m.mu.Lock()
	defer m.mu.Unlock()

	return State{
		Slots:         m.slots,
		ActiveSlot:    m.activeSlot,
		Floor:         m.floor,
		TrialBoots:    m.trialBoots,
		LastBootTrial: m.lastBootTrial,
	}
}
