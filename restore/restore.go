package restore

import "errors"

var (
	ErrInvalid   = errors.New("restore: invalid argument")
	ErrRole      = errors.New("restore: insufficient role")
	ErrClock     = errors.New("restore: clock moved backwards")
	ErrState     = errors.New("restore: invalid state")
	ErrNoBackup  = errors.New("restore: backup does not exist")
	ErrRestoring = errors.New("restore: system is restoring")
)

type Manager struct {
	systems    int
	nextBackup int
	backups    []backup
	ackHistory []map[int]int
	pending    []map[int]struct{}
	restoring  []bool
}

type backup struct {
	system int
	at     int
}

func NewManager(systems int) *Manager {
	m := &Manager{
		systems:    systems,
		ackHistory: make([]map[int]int, systems+1),
		pending:    make([]map[int]struct{}, systems+1),
		restoring:  make([]bool, systems+1),
	}
	for system := 1; system <= systems; system++ {
		m.ackHistory[system] = make(map[int]int)
	}
	return m
}

func (m *Manager) Backup(role, system, now int) (int, error) {
	if role < 1 || role > 3 || system < 1 || system > m.systems || now < 0 {
		return 0, ErrInvalid
	}
	if role != 3 {
		return 0, ErrRole
	}
	m.nextBackup++
	m.backups = append(m.backups, backup{system: system, at: now})
	return m.nextBackup, nil
}

func (m *Manager) Restore(role, system, backup, now int) ([]int, error) {
	if role < 1 || role > 3 || system < 1 || system > m.systems || backup < 1 || backup > m.nextBackup || now < 0 {
		return nil, ErrInvalid
	}
	if role != 3 {
		return nil, ErrRole
	}
	if m.backups[backup-1].system != system {
		return nil, ErrNoBackup
	}
	if m.restoring[system] {
		return nil, ErrState
	}
	backupTime := m.backups[backup-1].at
	replay := make([]int, 0)
	for erasure, ackTime := range m.ackHistory[system] {
		if ackTime > backupTime {
			replay = append(replay, erasure)
		}
	}
	sortInts(replay)
	m.pending[system] = make(map[int]struct{}, len(replay))
	if len(replay) > 0 {
		for _, erasure := range replay {
			m.pending[system][erasure] = struct{}{}
		}
		m.restoring[system] = true
	}
	return replay, nil
}

func (m *Manager) ReapplyDone(role, system, erasure, now int) error {
	if role < 1 || role > 3 || system < 1 || system > m.systems || erasure < 1 || now < 0 {
		return ErrInvalid
	}
	if role != 3 {
		return ErrRole
	}
	if !m.restoring[system] {
		return ErrState
	}
	if _, ok := m.pending[system][erasure]; !ok {
		return ErrState
	}
	delete(m.pending[system], erasure)
	if len(m.pending[system]) == 0 {
		m.restoring[system] = false
	}
	return nil
}

func (m *Manager) Read(system int) error {
	if system < 1 || system > m.systems {
		return ErrInvalid
	}
	if m.restoring[system] {
		return ErrRestoring
	}
	return nil
}

func (m *Manager) RecordAck(erasure, system, now int) {
	m.ackHistory[system][erasure] = now
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}
