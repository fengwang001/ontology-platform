package migration

import "errors"

var (
	ErrInvalidArgument  = errors.New("migration: invalid argument")
	ErrNotFound         = errors.New("migration: key not found")
	ErrMissingMigration = errors.New("migration: missing migration")
	ErrMigrationFailed  = errors.New("migration: migration failed")
)
