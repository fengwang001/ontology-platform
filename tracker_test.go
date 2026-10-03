package ontology

import (
	"slices"
	"testing"
)

func mustNewTracker(t *testing.T, softLimit int) *Tracker {
	t.Helper()

	tracker, err := NewTracker(softLimit)
	if err != nil {
		t.Fatalf("NewTracker(%d): %v", softLimit, err)
	}
	return tracker
}

func receive(t *testing.T, tracker *Tracker, msg, rcpt string, kind ReceiptKind, attempt int, ts int64, want ReceiptResult) {
	t.Helper()

	got, err := tracker.Receipt([]byte(msg), []byte(rcpt), kind, attempt, ts)
	if err != nil {
		t.Fatalf("Receipt(%s,%s,%s,%d,%d): %v", msg, rcpt, kind, attempt, ts, err)
	}
	t.Logf("input=Receipt msg=%q rcpt=%q kind=%s attempt=%d ts=%d output=%s basis=%s",
		msg, rcpt, kind, attempt, ts, got, decisionBasis(got))
	if got != want {
		t.Fatalf("Receipt(%s,%s,%s,%d,%d) = %s, want %s", msg, rcpt, kind, attempt, ts, got, want)
	}
}

func decisionBasis(result ReceiptResult) string {
	switch result {
	case Applied:
		return "receipt advanced state or recorded a decisive failure"
	case Stale:
		return "receipt was new but did not advance state"
	case Duplicate:
		return "kind and attempt pair was already seen"
	case Ignored:
		return "receipt is incompatible with the current failure state"
	default:
		return "unknown result"
	}
}

func mustTick(t *testing.T, tracker *Tracker, now int64) []Expiration {
	t.Helper()

	got, err := tracker.Tick(now)
	if err != nil {
		t.Fatalf("Tick(%d): %v", now, err)
	}
	t.Logf("input=Tick now=%d output=%v basis=deadline <= now and not already expired", now, got)
	return got
}

func storedRecipient(t *testing.T, tracker *Tracker, msg, rcpt string) *recipient {
	t.Helper()

	storedMsg := tracker.messages[msg]
	if storedMsg == nil {
		t.Fatalf("message %q does not exist", msg)
	}
	storedRcpt := storedMsg.recipients[rcpt]
	if storedRcpt == nil {
		t.Fatalf("recipient %q does not exist", rcpt)
	}
	return storedRcpt
}

func assertRcpt(t *testing.T, tracker *Tracker, msg, rcpt string, r int, fail Failure, late bool) {
	t.Helper()

	got := storedRecipient(t, tracker, msg, rcpt)
	if got.r != r || got.fail != fail || got.late != late {
		t.Fatalf("%s/%s = (r=%d fail=%s late=%v), want (r=%d fail=%s late=%v)",
			msg, rcpt, got.r, got.fail, got.late, r, fail, late)
	}
}

func expirationEqual(a, b Expiration) bool {
	return slices.Equal(a.Message, b.Message) && slices.Equal(a.Recipient, b.Recipient)
}
