package ontology

import (
	"errors"
	"sync"
)

var (
	ErrInvalidConstructorArgs = errors.New("invalid constructor arguments")
	ErrInvalidVersion         = errors.New("invalid install version")
	ErrVersionNotRaised       = errors.New("install version must be greater than floor")
	ErrInactiveSlotOnTrial    = errors.New("inactive slot is already on trial")
	ErrNoTrialBoot            = errors.New("last boot was not a trial boot")
)

type SlotStatus int

const (
	SlotEmpty SlotStatus = iota
	SlotGood
	SlotBad
	SlotTrial
)

func (status SlotStatus) String() string {
	switch status {
	case SlotGood:
		return "Good"
	case SlotBad:
		return "Bad"
	case SlotTrial:
		return "Trial"
	default:
		return "Empty"
	}
}

type SlotState struct {
	Status         SlotStatus
	Version        int
	TriesRemaining int
}

type BootResult struct {
	Slot     int
	Version  int
	Trial    bool
	Rollback bool
}

type Snapshot struct {
	Slots            [2]SlotState
	ActiveSlot       int
	Floor            int
	LastBootWasTrial bool
}

type UpgradeManager struct {
	mu               sync.RWMutex
	slots            [2]SlotState
	activeSlot       int
	floor            int
	maxTrialBoots    int
	lastBootWasTrial bool
	lastTrialSlot    int
}

func NewUpgradeManager(initialVersion int, maxTrialBoots int) (*UpgradeManager, error) {
	if initialVersion < 1 || maxTrialBoots < 1 {
		return nil, ErrInvalidConstructorArgs
	}

	return &UpgradeManager{
		slots: [2]SlotState{
			{Status: SlotGood, Version: initialVersion},
			{Status: SlotEmpty, Version: 0},
		},
		activeSlot:    0,
		floor:         initialVersion,
		maxTrialBoots: maxTrialBoots,
		lastTrialSlot: -1,
	}, nil
}

func (m *UpgradeManager) Install(version int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if version == 0 {
		return ErrInvalidVersion
	}

	if version <= m.floor {
		return ErrVersionNotRaised
	}

	inactiveSlot := 1 - m.activeSlot
	if m.slots[inactiveSlot].Status == SlotTrial {
		return ErrInactiveSlotOnTrial
	}

	m.slots[inactiveSlot] = SlotState{
		Status:         SlotTrial,
		Version:        version,
		TriesRemaining: m.maxTrialBoots,
	}

	return nil
}

func (m *UpgradeManager) Boot() BootResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	inactiveSlot := 1 - m.activeSlot
	if m.slots[inactiveSlot].Status != SlotTrial {
		m.lastBootWasTrial = false
		m.lastTrialSlot = -1
		return BootResult{
			Slot:    m.activeSlot,
			Version: m.slots[m.activeSlot].Version,
		}
	}

	if m.slots[inactiveSlot].TriesRemaining > 0 {
		m.slots[inactiveSlot].TriesRemaining--
		m.lastBootWasTrial = true
		m.lastTrialSlot = inactiveSlot
		return BootResult{
			Slot:    inactiveSlot,
			Version: m.slots[inactiveSlot].Version,
			Trial:   true,
		}
	}

	m.slots[inactiveSlot].Status = SlotBad
	m.lastBootWasTrial = false
	m.lastTrialSlot = -1
	return BootResult{
		Slot:     m.activeSlot,
		Version:  m.slots[m.activeSlot].Version,
		Rollback: true,
	}
}

func (m *UpgradeManager) Confirm() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.lastBootWasTrial {
		return ErrNoTrialBoot
	}

	trialSlot := m.lastTrialSlot
	m.slots[trialSlot].Status = SlotGood
	m.slots[trialSlot].TriesRemaining = 0
	m.activeSlot = trialSlot
	m.floor = m.slots[trialSlot].Version
	m.lastBootWasTrial = false
	m.lastTrialSlot = -1

	return nil
}

func (m *UpgradeManager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return Snapshot{
		Slots:            m.slots,
		ActiveSlot:       m.activeSlot,
		Floor:            m.floor,
		LastBootWasTrial: m.lastBootWasTrial,
	}
}
