package halfmessage

import (
	"errors"
	"sync"
)

var (
	ErrClockMovedBack       = errors.New("halfmessage: clock moved back")
	ErrInvalidConfig        = errors.New("halfmessage: invalid configuration")
	ErrUnknownTransaction   = errors.New("halfmessage: unknown transaction")
	ErrDuplicateTransaction = errors.New("halfmessage: duplicate transaction")
	ErrAlreadyCommitted     = errors.New("halfmessage: transaction already committed")
	ErrAlreadyRolledBack    = errors.New("halfmessage: transaction already rolled back")
)

type Status int

const (
	Pending Status = iota
	Committed
	RolledBack
)

type RollbackReason int

const (
	RollbackExplicit RollbackReason = iota
	RollbackChecksExhausted
)

type CheckDecision int

const (
	CheckUnknown CheckDecision = iota
	CheckCommit
	CheckRollback
)

type Config struct {
	FirstCheckAfter int64
	CheckInterval   int64
	MaxChecks       uint64
}

type CheckCallback[T any] func(txID string, payload T, attempt uint64, now int64) CheckDecision

type CommittedMessage[T any] struct {
	Position uint64
	TxID     string
	Payload  T
}

type MessageInfo[T any] struct {
	TxID           string
	Payload        T
	Status         Status
	Position       uint64
	Attempts       uint64
	CreatedAt      int64
	NextCheckAt    int64
	RollbackReason RollbackReason
}

type Log[T any] struct {
	tickMu    sync.Mutex
	mu        sync.Mutex
	cfg       Config
	callback  CheckCallback[T]
	now       int64
	created   []*message[T]
	messages  map[string]*message[T]
	committed []CommittedMessage[T]
}

type message[T any] struct {
	id             string
	payload        T
	status         Status
	position       uint64
	attempts       uint64
	createdAt      int64
	nextCheckAt    int64
	rollbackReason RollbackReason
}

func New[T any](cfg Config, callback CheckCallback[T]) (*Log[T], error) {
	if cfg.FirstCheckAfter < 0 || cfg.CheckInterval < 1 || cfg.MaxChecks < 1 {
		return nil, ErrInvalidConfig
	}
	if callback == nil {
		return nil, ErrInvalidConfig
	}
	return &Log[T]{
		cfg:      cfg,
		callback: callback,
		messages: make(map[string]*message[T]),
	}, nil
}

func (l *Log[T]) Send(txID string, payload T, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.checkNow(now); err != nil {
		return err
	}
	if _, ok := l.messages[txID]; ok {
		return ErrDuplicateTransaction
	}
	msg := &message[T]{
		id:          txID,
		payload:     payload,
		status:      Pending,
		createdAt:   now,
		nextCheckAt: now + l.cfg.FirstCheckAfter,
	}
	l.created = append(l.created, msg)
	l.messages[txID] = msg
	return nil
}

func (l *Log[T]) Commit(txID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	msg, err := l.terminalGuard(txID)
	if err != nil {
		return err
	}
	l.commitLocked(msg)
	return nil
}

func (l *Log[T]) Rollback(txID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	msg, err := l.terminalGuard(txID)
	if err != nil {
		return err
	}
	l.rollbackLocked(msg, RollbackExplicit)
	return nil
}

func (l *Log[T]) Tick(now int64) error {
	l.tickMu.Lock()
	defer l.tickMu.Unlock()

	l.mu.Lock()

	if err := l.checkNow(now); err != nil {
		l.mu.Unlock()
		return err
	}
	due := make([]*message[T], 0)
	for _, msg := range l.created {
		if msg.status == Pending && msg.nextCheckAt <= now {
			due = append(due, msg)
		}
	}
	l.mu.Unlock()

	for _, msg := range due {
		l.mu.Lock()
		if msg.status != Pending {
			l.mu.Unlock()
			continue
		}
		msg.attempts++
		attempt := msg.attempts
		txID := msg.id
		payload := msg.payload
		l.mu.Unlock()

		decision := l.callback(txID, payload, attempt, now)

		l.mu.Lock()
		if msg.status != Pending {
			l.mu.Unlock()
			continue
		}
		switch {
		case attempt >= l.cfg.MaxChecks && decision == CheckUnknown:
			l.rollbackLocked(msg, RollbackChecksExhausted)
		case decision == CheckCommit:
			l.commitLocked(msg)
		case decision == CheckRollback:
			l.rollbackLocked(msg, RollbackExplicit)
		default:
			msg.nextCheckAt = now + l.cfg.CheckInterval
		}
		l.mu.Unlock()
	}
	return nil
}

func (l *Log[T]) Messages() []MessageInfo[T] {
	l.mu.Lock()
	defer l.mu.Unlock()

	infos := make([]MessageInfo[T], 0, len(l.created))
	for _, msg := range l.created {
		infos = append(infos, MessageInfo[T]{
			TxID:           msg.id,
			Payload:        msg.payload,
			Status:         msg.status,
			Position:       msg.position,
			Attempts:       msg.attempts,
			CreatedAt:      msg.createdAt,
			NextCheckAt:    msg.nextCheckAt,
			RollbackReason: msg.rollbackReason,
		})
	}
	return infos
}

func (l *Log[T]) CommittedMessages() []CommittedMessage[T] {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]CommittedMessage[T](nil), l.committed...)
}

func (l *Log[T]) checkNow(now int64) error {
	if now < l.now {
		return ErrClockMovedBack
	}
	l.now = now
	return nil
}

func (l *Log[T]) terminalGuard(txID string) (*message[T], error) {
	msg, ok := l.messages[txID]
	if !ok {
		return nil, ErrUnknownTransaction
	}
	switch msg.status {
	case Committed:
		return nil, ErrAlreadyCommitted
	case RolledBack:
		return nil, ErrAlreadyRolledBack
	default:
		return msg, nil
	}
}

func (l *Log[T]) commitLocked(msg *message[T]) {
	msg.status = Committed
	msg.position = uint64(len(l.committed))
	l.committed = append(l.committed, CommittedMessage[T]{
		Position: msg.position,
		TxID:     msg.id,
		Payload:  msg.payload,
	})
}

func (l *Log[T]) rollbackLocked(msg *message[T], reason RollbackReason) {
	msg.status = RolledBack
	msg.rollbackReason = reason
}
