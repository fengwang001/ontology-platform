package source

import (
	"context"
	"errors"
)

type Source interface {
	Length(ctx context.Context) (int64, error)
	ReadAt(ctx context.Context, p []byte, off int64) (int, error)
}

var ErrShortEOF = errors.New("source: unexpected EOF with fewer bytes than requested")

