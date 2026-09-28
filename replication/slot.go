// Package replication implements a logical replication slot that tracks a
// confirmed position and a restart position over an interleaved, append-only
// transaction log and can re-deliver committed-but-unconfirmed transactions
// after a crash.
package replication

import (
	"context"
)

// Kind identifies the kind of a log record.
type Kind uint8

const (
	KindBegin  Kind = 1
	KindData   Kind = 2
	KindCommit Kind = 3
	KindAbort  Kind = 4
)

// Record is one log record carried by an interleaved transaction stream.
type Record struct {
	LSN     int64
	TxID    string
	Kind    Kind
	Payload []byte
}

// Transaction is a fully assembled transaction handed to a subscriber when its
// commit record is decoded.
type Transaction struct {
	TxID      string
	BeginLSN  int64
	CommitLSN int64
	Records   []Record
}

// Emitter receives transactions in commit order. Emit is invoked while the
// slot lock is held, so implementations must not call back into the slot.
type Emitter interface {
	Emit(context.Context, Transaction)
}

// EmitterFunc adapts a function to Emitter.
type EmitterFunc func(context.Context, Transaction)

func (f EmitterFunc) Emit(ctx context.Context, tx Transaction) { f(ctx, tx) }

// Config configures a slot opened with Open.
type Config struct {
	// Dir is the directory that holds the durable WAL and state files.
	Dir string
	// MaxInFlight is the maximum number of simultaneously open transactions
	// the decoder tolerates. Zero means a built-in default.
	MaxInFlight int
	// Emitter receives every transaction at commit time, including the
	// committed-but-unconfirmed transactions replayed on restart.
	Emitter Emitter
}

// Slot is a durable logical replication slot.
type Slot struct {
	// fields are added in later increments.
}

// ErrorCode enumerates the distinguishable rejection reasons.
type ErrorCode string

const (
	ErrInvalidRecord  ErrorCode = "invalid_record"
ErrAckNotBoundary ErrorCode = "ack_not_commit_boundary"
	ErrAckRewound     ErrorCode = "ack_rewound"
	ErrInFlightLimit  ErrorCode = "in_flight_limit"
)

// SlotError is returned for every rejected operation. Code lets callers tell
// rejection reasons apart without matching on message text.
type SlotError struct {
	Code ErrorCode
	Msg  string
}

func (e *SlotError) Error() string { return string(e.Code) + ": " + e.Msg }

// Open opens or recovers a slot in cfg.Dir.
func Open(ctx context.Context, cfg Config) (*Slot, error) {
	return nil, nil
}

// Close releases the slot's resources.
func (s *Slot) Close() error { return nil }

// Append decodes one log record. It must be called in ascending LSN order.
func (s *Slot) Append(ctx context.Context, rec Record) error { return nil }

// Acknowledge advances the confirmed position to the commit boundary lsn.
func (s *Slot) Acknowledge(ctx context.Context, lsn int64) error { return nil }

// Positions returns the current restart and confirmed positions.
func (s *Slot) Positions() (restartLSN, confirmedLSN int64) { return 0, 0 }

// Restart drops and re-opens the slot, replaying the retained WAL, which
// re-emits every committed-but-unconfirmed transaction.
func (s *Slot) Restart(ctx context.Context) error { return nil }
