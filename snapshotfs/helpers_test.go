package snapshotfs

import "errors"

func errName(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, ErrInvalidArgument):
		return "ErrInvalidArgument"
	case errors.Is(err, ErrOutOfSpace):
		return "ErrOutOfSpace"
	case errors.Is(err, ErrNotFound):
		return "ErrNotFound"
	case errors.Is(err, ErrDiscarded):
		return "ErrDiscarded"
	case errors.Is(err, ErrDead):
		return "ErrDead"
	case errors.Is(err, ErrSnapshotExists):
		return "ErrSnapshotExists"
	case errors.Is(err, ErrSnapshotMissing):
		return "ErrSnapshotMissing"
	case errors.Is(err, ErrNotHeld):
		return "ErrNotHeld"
	case errors.Is(err, ErrHeld):
		return "ErrHeld"
	default:
		return err.Error()
	}
}
