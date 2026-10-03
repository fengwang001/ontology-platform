package ontology

import (
	"bytes"
	"slices"
	"sort"
)

type oracleTracker struct {
	softLimit int
	messages  map[string]*oracleMessage
	lastTick  int64
}

type oracleMessage struct {
	deadline   int64
	order      []string
	recipients map[string]*oracleRecipient
}

type oracleRecipient struct {
	r       int
	fail    Failure
	sentA   int
	softSet map[int]bool
	seen    map[receiptID]bool
	late    bool
}

type oracleSnapshot struct {
	r       int
	fail    Failure
	sentA   int
	late    bool
	softSet []int
	seen    []receiptID
}

func newOracleTracker(softLimit int) *oracleTracker {
	return &oracleTracker{softLimit: softLimit, messages: make(map[string]*oracleMessage)}
}

func (o *oracleTracker) send(msg string, rcpts []string, deadline int64) error {
	if _, exists := o.messages[msg]; exists {
		return ErrMessageExists
	}
	storedMsg := &oracleMessage{
		deadline:   deadline,
		order:      slices.Clone(rcpts),
		recipients: make(map[string]*oracleRecipient),
	}
	for _, rcpt := range rcpts {
		storedMsg.recipients[rcpt] = &oracleRecipient{
			softSet: make(map[int]bool),
			seen:    make(map[receiptID]bool),
		}
	}
	o.messages[msg] = storedMsg
	return nil
}

func (o *oracleTracker) receipt(msg, rcpt string, kind ReceiptKind, attempt int, ts int64) ReceiptResult {
	storedMsg := o.messages[msg]
	storedRcpt := storedMsg.recipients[rcpt]
	id := receiptID{kind: kind, attempt: attempt}
	if storedRcpt.seen[id] {
		return Duplicate
	}
	storedRcpt.seen[id] = true

	if kind == Sent || kind == Delivered || kind == Read {
		progress := map[ReceiptKind]int{Sent: 1, Delivered: 2, Read: 3}[kind]
		if storedRcpt.fail == SoftFail || storedRcpt.fail == HardFail {
			return Ignored
		}
		if storedRcpt.fail == Expired {
			if (kind == Delivered || kind == Read) && ts < storedMsg.deadline {
				storedRcpt.fail = NoFailure
				storedRcpt.r = max(storedRcpt.r, progress)
				return Applied
			}
			return Ignored
		}
		if kind == Sent {
			storedRcpt.sentA = max(storedRcpt.sentA, attempt)
		}
		if progress > storedRcpt.r {
			storedRcpt.r = progress
			return Applied
		}
		return Stale
	}

	if kind == Soft {
		if storedRcpt.fail != NoFailure {
			return Ignored
		}
		if storedRcpt.r >= 2 {
			return Stale
		}
		storedRcpt.softSet[attempt] = true
		count := 0
		for softAttempt := range storedRcpt.softSet {
			if softAttempt >= storedRcpt.sentA {
				count++
			}
		}
		if count >= o.softLimit {
			storedRcpt.fail = SoftFail
		}
		return Applied
	}

	if storedRcpt.fail == SoftFail || storedRcpt.fail == HardFail {
		return Ignored
	}
	if storedRcpt.r >= 2 {
		storedRcpt.late = true
		return Stale
	}
	storedRcpt.fail = HardFail
	return Applied
}

func (o *oracleTracker) tick(now int64) []Expiration {
	var got []Expiration
	if now == o.lastTick {
		return got
	}
	for msgName, storedMsg := range o.messages {
		if storedMsg.deadline <= now {
			for _, rcpt := range storedMsg.order {
				storedRcpt := storedMsg.recipients[rcpt]
				if storedRcpt.fail == NoFailure && storedRcpt.r < 2 {
					storedRcpt.fail = Expired
					got = append(got, Expiration{Message: []byte(msgName), Recipient: []byte(rcpt)})
				}
			}
		}
	}
	o.lastTick = now
	sort.Slice(got, func(i, j int) bool {
		if cmp := bytes.Compare(got[i].Message, got[j].Message); cmp != 0 {
			return cmp < 0
		}
		return bytes.Compare(got[i].Recipient, got[j].Recipient) < 0
	})
	return got
}

func (o *oracleTracker) snapshot(msg string) []oracleSnapshot {
	storedMsg := o.messages[msg]
	got := make([]oracleSnapshot, 0, len(storedMsg.order))
	for _, rcpt := range storedMsg.order {
		storedRcpt := storedMsg.recipients[rcpt]
		got = append(got, oracleSnapshot{
			r:       storedRcpt.r,
			fail:    storedRcpt.fail,
			sentA:   storedRcpt.sentA,
			late:    storedRcpt.late,
			softSet: sortedInts(storedRcpt.softSet),
			seen:    sortedIDs(storedRcpt.seen),
		})
	}
	return got
}

func productionSnapshot(tracker *Tracker, msg string) []oracleSnapshot {
	storedMsg := tracker.messages[msg]
	got := make([]oracleSnapshot, 0, len(storedMsg.order))
	for _, storedRcpt := range storedMsg.order {
		softValues := make(map[int]bool, len(storedRcpt.softSet))
		for value := range storedRcpt.softSet {
			softValues[value] = true
		}
		seenValues := make(map[receiptID]bool, len(storedRcpt.seen))
		for value := range storedRcpt.seen {
			seenValues[value] = true
		}
		got = append(got, oracleSnapshot{
			r:       storedRcpt.r,
			fail:    storedRcpt.fail,
			sentA:   storedRcpt.sentA,
			late:    storedRcpt.late,
			softSet: sortedInts(softValues),
			seen:    sortedIDs(seenValues),
		})
	}
	return got
}

func sortedInts(values map[int]bool) []int {
	got := make([]int, 0, len(values))
	for value := range values {
		got = append(got, value)
	}
	sort.Ints(got)
	return got
}

func sortedIDs(values map[receiptID]bool) []receiptID {
	got := make([]receiptID, 0, len(values))
	for value := range values {
		got = append(got, value)
	}
	sort.Slice(got, func(i, j int) bool {
		if got[i].kind != got[j].kind {
			return got[i].kind < got[j].kind
		}
		return got[i].attempt < got[j].attempt
	})
	return got
}
