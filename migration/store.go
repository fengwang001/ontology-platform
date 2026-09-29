// Package migration implements keyed state with lazy per-key schema migration.
package migration

import (
	"context"
	"errors"
)

// MigrateFunc transforms a value from one schema version to the next.
type MigrateFunc[T any] func(ctx context.Context, key string, prev T) (T, error)

// CloneFunc returns an independent deep copy of a value.
type CloneFunc[T any] func(v T) T

// Logger receives per-step diagnostic lines.
type Logger interface {
	Printf(format string, args ...any)
}

// Store holds keyed values at per-key schema versions and migrates them lazily.
type Store[T any] struct{}

// Option configures a Store.
type Option[T any] func(*config)

// Sentinel error categories. Every rejected call leaves all state untouched.
var (
	ErrInvalidVersion  = errors.New("migration: invalid version")
	ErrInvalidKey      = errors.New("migration: invalid key")
	ErrKeyNotFound     = errors.New("migration: key not found")
	ErrMissingMigrate  = errors.New("migration: missing migration in chain")
	ErrMigrationFailed = errors.New("migration: migration function failed")
	ErrDowngrade       = errors.New("migration: current version cannot be downgraded")
	ErrAlreadyAtVersion = errors.New("migration: already at requested version")
	ErrDuplicateMigration = errors.New("migration: migration already registered for source version")
	ErrSourceVersion = errors.New("migration: source version out of range")
	ErrNewerStored   = errors.New("migration: stored version newer than current version")
)

type config struct{}

// WithClone supplies the deep-copy function used to isolate stored values.
func WithClone[T any](clone CloneFunc[T]) Option[T] { return nil }

// WithLogger redirects per-step diagnostic output.
func WithLogger[T any](logger Logger) Option[T] { return nil }

// New creates an empty store at initialVersion (must be >= 1).
func New[T any](initialVersion int, opts ...Option[T]) *Store[T] { return nil }

// Register registers the migration from version source to source+1.
func (s *Store[T]) Register(source int, fn MigrateFunc[T]) error { return nil }

// Upgrade advances the current schema version without touching any keys.
func (s *Store[T]) Upgrade(target int) error { return nil }

// CurrentVersion returns the current schema version.
func (s *Store[T]) CurrentVersion() int { return 0 }

// Write stores an independent copy of value at the current version.
func (s *Store[T]) Write(ctx context.Context, key string, value T) error { return nil }

// Read returns the value at the current version, migrating lazily if needed.
func (s *Store[T]) Read(ctx context.Context, key string) (T, error) {
	var zero T
	return zero, nil
}

// StoredVersion reports the on-disk version of a key without migrating it.
func (s *Store[T]) StoredVersion(key string) (int, bool, error) { return 0, false, nil }

// Check verifies that every stored key has a complete migration chain.
func (s *Store[T]) Check(ctx context.Context) error { return nil }

