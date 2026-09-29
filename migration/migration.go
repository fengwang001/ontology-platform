package migration

import (
	"fmt"
	"sync"
)

type MigrationFunc func(value []byte) (migrated []byte, err error)

type Logger interface {
	Printf(format string, args ...any)
}

type entry struct {
	version int
	value   []byte
}

type flight struct {
	done  chan struct{}
	value []byte
	err   error
}

type Store[K comparable] struct {
	mu         sync.Mutex
	gate       sync.RWMutex
	migrations map[int]MigrationFunc
	data       map[K]entry
	flights    map[K]*flight
	current    int
	logger     Logger
}

func NewStore[K comparable](currentVersion int, logger Logger) (*Store[K], error) {
	if currentVersion < 0 {
		return nil, fmt.Errorf("%w: current version must not be negative", ErrInvalidArgument)
	}

	return &Store[K]{
		migrations: make(map[int]MigrationFunc),
		data:       make(map[K]entry),
		flights:    make(map[K]*flight),
		current:    currentVersion,
		logger:     logger,
	}, nil
}

func (s *Store[K]) CurrentVersion() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.current
}

func (s *Store[K]) StoredVersion(key K) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, ok := s.data[key]
	if !ok {
		return 0, fmt.Errorf("%w: key %v", ErrNotFound, key)
	}

	return current.version, nil
}

func (s *Store[K]) RegisterMigration(fromVersion int, migrate MigrationFunc) error {
	if fromVersion < 0 {
		return fmt.Errorf("%w: source version must not be negative", ErrInvalidArgument)
	}
	if migrate == nil {
		return fmt.Errorf("%w: migration function must not be nil", ErrInvalidArgument)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.migrations[fromVersion]; exists {
		return fmt.Errorf("%w: migration from version %d is already registered", ErrInvalidArgument, fromVersion)
	}

	s.migrations[fromVersion] = migrate
	return nil
}

func (s *Store[K]) Upgrade(newVersion int) error {
	if newVersion < 0 {
		return fmt.Errorf("%w: new version must not be negative", ErrInvalidArgument)
	}

	s.gate.Lock()
	defer s.gate.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if newVersion <= s.current {
		return fmt.Errorf("%w: new version %d must be greater than current version %d", ErrInvalidArgument, newVersion, s.current)
	}

	s.current = newVersion
	return nil
}

func (s *Store[K]) Write(key K, value []byte) error {
	if value == nil {
		return fmt.Errorf("%w: value must not be nil", ErrInvalidArgument)
	}

	s.gate.RLock()
	defer s.gate.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if active := s.flights[key]; active != nil {
		s.mu.Unlock()
		<-active.done
		s.mu.Lock()
	}

	s.data[key] = entry{
		version: s.current,
		value:   cloneBytes(value),
	}
	return nil
}

func (s *Store[K]) Read(key K) ([]byte, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()

	var active *flight
	s.mu.Lock()
	for {
		if pending := s.flights[key]; pending != nil {
			active = pending
			s.mu.Unlock()
			<-active.done
			if active.err != nil {
				return nil, active.err
			}
			return cloneBytes(active.value), nil
		}

		stored, ok := s.data[key]
		if !ok {
			s.mu.Unlock()
			return nil, fmt.Errorf("%w: key %v", ErrNotFound, key)
		}
		if stored.version > s.current {
			s.mu.Unlock()
			return nil, fmt.Errorf("%w: stored version %d is newer than current version %d", ErrInvalidArgument, stored.version, s.current)
		}
		if stored.version == s.current {
			value := cloneBytes(stored.value)
			s.mu.Unlock()
			return value, nil
		}

		if err := s.checkChainLocked(stored.version, s.current); err != nil {
			s.mu.Unlock()
			return nil, err
		}

		active = &flight{done: make(chan struct{})}
		s.flights[key] = active

		migrations := make([]MigrationFunc, 0, s.current-stored.version)
		for version := stored.version; version < s.current; version++ {
			migrations = append(migrations, s.migrations[version])
		}
		storedVersion := stored.version
		currentVersion := s.current
		initial := cloneBytes(stored.value)
		ownerKey := key
		s.mu.Unlock()

		go s.runMigration(ownerKey, storedVersion, currentVersion, initial, migrations, active)

		<-active.done
		if active.err != nil {
			return nil, active.err
		}
		return cloneBytes(active.value), nil
	}
}

func (s *Store[K]) runMigration(key K, storedVersion, currentVersion int, initial []byte, migrations []MigrationFunc, active *flight) {
	value := initial
	var runErr error

	for version, migrate := range migrations {
		fromVersion := storedVersion + version
		input := cloneBytes(value)
		s.logf("migration step input: key=%v from=%d to=%d input=%q", key, fromVersion, fromVersion+1, input)

		migrated, err := migrate(input)
		if err != nil {
			runErr = fmt.Errorf("%w: from version %d: %v", ErrMigrationFailed, fromVersion, err)
			s.logf("migration step return: key=%v from=%d to=%d error=%v", key, fromVersion, fromVersion+1, err)
			s.logf("migration step decision: key=%v from=%d result=failed err=%v; storage remains unchanged", key, fromVersion, err)
			break
		}
		if migrated == nil {
			err := fmt.Errorf("migration from version %d returned nil", fromVersion)
			runErr = fmt.Errorf("%w: %w", ErrMigrationFailed, err)
			s.logf("migration step return: key=%v from=%d to=%d output=<nil>", key, fromVersion, fromVersion+1)
			s.logf("migration step decision: key=%v from=%d result=failed err=%v; storage remains unchanged", key, fromVersion, err)
			break
		}

		value = migrated
		s.logf("migration step return: key=%v from=%d to=%d output=%q", key, fromVersion, fromVersion+1, value)
		s.logf("migration step decision: key=%v from=%d result=success reason=function returned a non-nil value", key, fromVersion)
	}

	s.mu.Lock()
	if s.flights[key] == active {
		delete(s.flights, key)
	}

	if runErr == nil {
		latest, exists := s.data[key]
		if exists && latest.version == storedVersion && equalBytes(latest.value, initial) {
			s.data[key] = entry{
				version: currentVersion,
				value:   cloneBytes(value),
			}
			s.logf("migration write-back decision: key=%v from=%d to=%d result=committed reason=stored snapshot was unchanged", key, storedVersion, currentVersion)
		} else {
			s.logf("migration write-back decision: key=%v from=%d to=%d result=skipped reason=stored snapshot changed during migration", key, storedVersion, currentVersion)
		}
	}

	active.value = value
	active.err = runErr
	close(active.done)
	s.mu.Unlock()
}

func (s *Store[K]) CheckIntegrity(fromVersion int) error {
	if fromVersion < 0 {
		return fmt.Errorf("%w: source version must not be negative", ErrInvalidArgument)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if fromVersion > s.current {
		return fmt.Errorf("%w: source version %d is newer than current version %d", ErrInvalidArgument, fromVersion, s.current)
	}

	return s.checkChainLocked(fromVersion, s.current)
}

func (s *Store[K]) checkChainLocked(fromVersion, toVersion int) error {
	for version := fromVersion; version < toVersion; version++ {
		if s.migrations[version] == nil {
			s.logf("migration chain decision: from=%d to=%d result=incomplete missing_from=%d; no migration function is called", fromVersion, toVersion, version)
			return fmt.Errorf("%w: migration from version %d", ErrMissingMigration, version)
		}
	}

	s.logf("migration chain decision: from=%d to=%d result=complete reason=every consecutive source version is registered", fromVersion, toVersion)
	return nil
}

func (s *Store[K]) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}

func cloneBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
