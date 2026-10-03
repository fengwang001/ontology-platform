package ontology

import (
	"bytes"
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrMessageExists    = errors.New("message already exists")
	ErrMessageNotFound  = errors.New("message not found")
	ErrUnknownRecipient = errors.New("recipient not found")
	ErrClockMovedBack   = errors.New("clock moved back")
)

type Tracker struct {
	mu        sync.Mutex
	softLimit int
	messages  map[string]*message
	lastTick  int64
}

func NewTracker(softLimit int) (*Tracker, error) {
	if softLimit < 1 || softLimit > 16 {
		return nil, ErrInvalidArgument
	}

	return &Tracker{
		softLimit: softLimit,
		messages:  make(map[string]*message),
	}, nil
}

func (t *Tracker) Send(msg []byte, rcpts [][]byte, deadline int64) error {
	if len(msg) == 0 || len(rcpts) < 1 || len(rcpts) > 32 || deadline < 0 || deadline > 1_000_000_000_000 {
		return ErrInvalidArgument
	}

	seen := make(map[string]struct{}, len(rcpts))
	for _, rcpt := range rcpts {
		if len(rcpt) == 0 {
			return ErrInvalidArgument
		}
		key := string(rcpt)
		if _, ok := seen[key]; ok {
			return ErrInvalidArgument
		}
		seen[key] = struct{}{}
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	key := string(msg)
	if _, ok := t.messages[key]; ok {
		return ErrMessageExists
	}

	storedMsg := &message{
		id:         cloneBytes(msg),
		deadline:   deadline,
		order:      make([]*recipient, 0, len(rcpts)),
		recipients: make(map[string]*recipient, len(rcpts)),
	}

	for _, rcpt := range rcpts {
		storedRcpt := &recipient{
			id:      cloneBytes(rcpt),
			softSet: make(map[int]struct{}),
			seen:    make(map[receiptID]struct{}),
		}
		storedMsg.order = append(storedMsg.order, storedRcpt)
		storedMsg.recipients[string(rcpt)] = storedRcpt
	}

	t.messages[key] = storedMsg
	return nil
}

func (t *Tracker) Receipt(msg []byte, rcpt []byte, kind ReceiptKind, attempt int, ts int64) (ReceiptResult, error) {
	if !validKind(kind) || attempt < 1 || attempt > 16 || ts < 0 || ts > 1_000_000_000_000 {
		return "", ErrInvalidArgument
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	storedMsg, ok := t.messages[string(msg)]
	if !ok {
		return "", ErrMessageNotFound
	}
	storedRcpt, ok := storedMsg.recipients[string(rcpt)]
	if !ok {
		return "", ErrUnknownRecipient
	}

	id := receiptID{kind: kind, attempt: attempt}
	if _, ok := storedRcpt.seen[id]; ok {
		return Duplicate, nil
	}
	storedRcpt.seen[id] = struct{}{}

	switch kind {
	case Sent, Delivered, Read:
		progress := progressFor(kind)
		switch storedRcpt.fail {
		case SoftFail, HardFail:
			return Ignored, nil
		case Expired:
			if (kind == Delivered || kind == Read) && ts < storedMsg.deadline {
				storedRcpt.fail = NoFailure
				storedRcpt.r = max(storedRcpt.r, progress)
				return Applied, nil
			}
			return Ignored, nil
		default:
			if kind == Sent {
				storedRcpt.sentA = max(storedRcpt.sentA, attempt)
			}
			if progress > storedRcpt.r {
				storedRcpt.r = progress
				return Applied, nil
			}
			return Stale, nil
		}
	case Soft:
		if storedRcpt.fail != NoFailure {
			return Ignored, nil
		}
		if storedRcpt.r >= 2 {
			return Stale, nil
		}
		storedRcpt.softSet[attempt] = struct{}{}
		count := 0
		for softAttempt := range storedRcpt.softSet {
			if softAttempt >= storedRcpt.sentA {
				count++
			}
		}
		if count >= t.softLimit {
			storedRcpt.fail = SoftFail
		}
		return Applied, nil
	case Hard:
		if storedRcpt.fail == SoftFail || storedRcpt.fail == HardFail {
			return Ignored, nil
		}
		if storedRcpt.r >= 2 {
			storedRcpt.late = true
			return Stale, nil
		}
		storedRcpt.fail = HardFail
		return Applied, nil
	default:
		return "", ErrInvalidArgument
	}
}

func (t *Tracker) Tick(now int64) ([]Expiration, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if now < t.lastTick {
		return nil, ErrClockMovedBack
	}

	var expirations []Expiration
	if now != t.lastTick {
		for _, storedMsg := range t.messages {
			if storedMsg.deadline > now {
				continue
			}
			for _, storedRcpt := range storedMsg.order {
				if storedRcpt.fail == NoFailure && storedRcpt.r < 2 {
					storedRcpt.fail = Expired
					expirations = append(expirations, Expiration{
						Message:   cloneBytes(storedMsg.id),
						Recipient: cloneBytes(storedRcpt.id),
					})
				}
			}
		}
	}

	t.lastTick = now
	sort.Slice(expirations, func(i, j int) bool {
		cmp := bytes.Compare(expirations[i].Message, expirations[j].Message)
		if cmp != 0 {
			return cmp < 0
		}
		return bytes.Compare(expirations[i].Recipient, expirations[j].Recipient) < 0
	})

	return expirations, nil
}

func (t *Tracker) Status(msg []byte) (MessageStatus, error) {
	if len(msg) == 0 {
		return MessageStatus{}, ErrInvalidArgument
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	storedMsg, ok := t.messages[string(msg)]
	if !ok {
		return MessageStatus{}, ErrMessageNotFound
	}

	total := len(storedMsg.order)
	failedCount := 0
	pending := 0
	allRead := true
	result := MessageStatus{
		Message:    cloneBytes(storedMsg.id),
		Recipients: make([]RecipientStatus, 0, total),
	}

	for _, storedRcpt := range storedMsg.order {
		if storedRcpt.fail != NoFailure {
			failedCount++
		}
		if storedRcpt.fail == NoFailure && storedRcpt.r < 2 {
			pending++
		}
		if storedRcpt.r != 3 {
			allRead = false
		}
		result.Recipients = append(result.Recipients, RecipientStatus{
			Recipient: cloneBytes(storedRcpt.id),
			R:         storedRcpt.r,
			Fail:      storedRcpt.fail,
			Late:      storedRcpt.late,
		})
	}

	switch {
	case failedCount == total:
		result.Status = Failed
	case pending > 0:
		result.Status = Inflight
	case failedCount > 0:
		result.Status = Partial
	case allRead:
		result.Status = ReadStatus
	default:
		result.Status = DeliveredStatus
	}

	return result, nil
}

func validKind(kind ReceiptKind) bool {
	switch kind {
	case Sent, Delivered, Read, Soft, Hard:
		return true
	default:
		return false
	}
}

func progressFor(kind ReceiptKind) int {
	switch kind {
	case Sent:
		return 1
	case Delivered:
		return 2
	case Read:
		return 3
	default:
		return 0
	}
}
