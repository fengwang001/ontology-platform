package netcode

import "testing"

func TestQuotaOverflowAndRejectedMoveConsumeQuota(t *testing.T) {
	server, err := NewServer(Config{WorldWidth: 10, Quota: 2, MaxStep: 2, Backlog: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.RegisterPlayer("p1", 0); err != nil {
		t.Fatal(err)
	}

	moves := []Move{{Sequence: 1, Delta: 1}, {Sequence: 2, Delta: 3}, {Sequence: 3, Delta: 1}}
	for _, move := range moves {
		if result := server.Submit("p1", move); !result.Accepted {
			t.Fatalf("Move %+v was not queued: %+v", move, result)
		}
	}

	firstTick := server.Tick(1)
	confirmation := firstTick.Confirmations["p1"]
	if confirmation.ProcessedSequence != 2 || confirmation.Position != 1 {
		t.Fatalf("unexpected first confirmation: %+v", confirmation)
	}
	if _, rejected := confirmation.RejectedSequences[2]; !rejected {
		t.Fatalf("sequence 2 should consume quota and be rejected: %+v", confirmation)
	}

	secondTick := server.Tick(2)
	confirmation = secondTick.Confirmations["p1"]
	if confirmation.ProcessedSequence != 3 || confirmation.Position != 2 {
		t.Fatalf("overflow input should process in next tick: %+v", confirmation)
	}
}

func TestClampIsAccepted(t *testing.T) {
	server, _ := NewServer(Config{WorldWidth: 10, Quota: 1, MaxStep: 10, Backlog: 1})
	if err := server.RegisterPlayer("p1", 8); err != nil {
		t.Fatal(err)
	}
	if result := server.Submit("p1", Move{Sequence: 1, Delta: 5}); !result.Accepted {
		t.Fatalf("receive rejected: %+v", result)
	}
	confirmation := server.Tick(1).Confirmations["p1"]
	if confirmation.Position != 10 || len(confirmation.RejectedSequences) != 0 {
		t.Fatalf("clamped move should be accepted: %+v", confirmation)
	}
}

func TestReceiptDuplicateGapAndBacklog(t *testing.T) {
	server, _ := NewServer(Config{WorldWidth: 10, Quota: 1, MaxStep: 10, Backlog: 1})
	if err := server.RegisterPlayer("p1", 0); err != nil {
		t.Fatal(err)
	}

	first := server.Submit("p1", Move{Sequence: 1, Delta: 1})
	duplicate := server.Submit("p1", Move{Sequence: 1, Delta: 1})
	if duplicate.Existing == nil || duplicate.Existing.Accepted != first.Accepted {
		t.Fatalf("duplicate must return previous receipt: first=%+v duplicate=%+v", first, duplicate)
	}
	if result := server.Submit("p1", Move{Sequence: 3, Delta: 1}); result.Reason != RejectionGap {
		t.Fatalf("expected gap, got %+v", result)
	}
	if result := server.Submit("p1", Move{Sequence: 2, Delta: 1}); result.Reason != RejectionBacklogFull {
		t.Fatalf("expected backlog full, got %+v", result)
	}

	server.Tick(1)
	if result := server.Submit("p1", Move{Sequence: 2, Delta: 1}); !result.Accepted {
		t.Fatalf("sequence 2 should be accepted after backlog drained: %+v", result)
	}
}

func TestValidationPriorityAndClockRewind(t *testing.T) {
	server, _ := NewServer(Config{WorldWidth: 10, Quota: 1, MaxStep: 5, Backlog: 2})
	if result := server.Submit("missing", Move{Sequence: 1, Delta: 0}); result.Reason != RejectionInvalidArgument {
		t.Fatalf("expected invalid argument, got %+v", result)
	}
	if err := server.RegisterPlayer("p1", 0); err != nil {
		t.Fatal(err)
	}
	server.Tick(10)
	if result := server.Tick(10); result.Accepted || result.Reason != RejectionClockRewound {
		t.Fatalf("equal timestamp should rewind: %+v", result)
	}
	if result := server.Submit("p1", Move{Sequence: 1, Delta: 0}); result.Reason != RejectionInvalidArgument {
		t.Fatalf("input validation must outrank clock state: %+v", result)
	}
}
