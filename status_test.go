package ontology

import "testing"

func TestAggregateStatuses(t *testing.T) {
	tracker := mustNewTracker(t, 2)

	send := func(msg string, rcpts ...string) {
		t.Helper()
		stored := make([][]byte, len(rcpts))
		for i, rcpt := range rcpts {
			stored[i] = []byte(rcpt)
		}
		if err := tracker.Send([]byte(msg), stored, 100); err != nil {
			t.Fatalf("Send(%q): %v", msg, err)
		}
	}

	send("failed", "A", "B")
	receive(t, tracker, "failed", "A", Hard, 1, 1, Applied)
	receive(t, tracker, "failed", "B", Soft, 1, 1, Applied)
	receive(t, tracker, "failed", "B", Soft, 2, 2, Applied)

	send("inflight", "A")

	send("partial", "ok", "bad")
	receive(t, tracker, "partial", "ok", Delivered, 1, 1, Applied)
	receive(t, tracker, "partial", "bad", Hard, 1, 1, Applied)

	send("delivered", "A")
	receive(t, tracker, "delivered", "A", Delivered, 1, 1, Applied)

	send("read", "A")
	receive(t, tracker, "read", "A", Read, 1, 1, Applied)

	for _, want := range []struct {
		msg    string
		status AggregateStatus
	}{
		{"failed", Failed},
		{"inflight", Inflight},
		{"partial", Partial},
		{"delivered", DeliveredStatus},
		{"read", ReadStatus},
	} {
		got, err := tracker.Status([]byte(want.msg))
		if err != nil {
			t.Fatalf("Status(%q): %v", want.msg, err)
		}
		t.Logf("input=Status msg=%q output=%s recipients=%v basis=N=%d F=%d pending=%d allRead=%v",
			want.msg, got.Status, got.Recipients, len(got.Recipients),
			countByFailure(got), countPending(got), allRead(got))
		if got.Status != want.status {
			t.Fatalf("Status(%q) = %s, want %s", want.msg, got.Status, want.status)
		}
	}
}

func countByFailure(status MessageStatus) int {
	count := 0
	for _, rcpt := range status.Recipients {
		if rcpt.Fail != NoFailure {
			count++
		}
	}
	return count
}

func countPending(status MessageStatus) int {
	count := 0
	for _, rcpt := range status.Recipients {
		if rcpt.Fail == NoFailure && rcpt.R < 2 {
			count++
		}
	}
	return count
}

func allRead(status MessageStatus) bool {
	for _, rcpt := range status.Recipients {
		if rcpt.R != 3 {
			return false
		}
	}
	return true
}
