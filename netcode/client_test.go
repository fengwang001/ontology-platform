package netcode

import (
	"reflect"
	"testing"
)

func (client *Client) pendingMoveForTest(sequence int64) Move {
	client.mu.Lock()
	defer client.mu.Unlock()
	for _, input := range client.pending {
		if input.move.Sequence == sequence {
			return input.move
		}
	}
	return Move{}
}

func TestConfirmationOrderAndRejectedReplay(t *testing.T) {
	config := Config{WorldWidth: 10, Quota: 1, MaxStep: 2, Backlog: 10}
	server, _ := NewServer(config)
	if err := server.RegisterPlayer("p1", 0); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(config, "p1", 0)
	if err != nil {
		t.Fatal(err)
	}

	moves := []Move{{Sequence: 1, Delta: 1}, {Sequence: 2, Delta: 3}, {Sequence: 3, Delta: 2}}
	for _, move := range moves {
		if result := server.Submit("p1", move); !result.Accepted {
			t.Fatalf("submit %+v: %+v", move, result)
		}
		if result := client.AddInput(move); !result.Accepted {
			t.Fatalf("predict %+v: %+v", move, result)
		}
	}
	if client.PredictedPosition() != 3 {
		t.Fatalf("oversized local input should be predicted as rejected, got %d", client.PredictedPosition())
	}

	first := server.Tick(1).Confirmations["p1"]
	second := server.Tick(2).Confirmations["p1"]
	third := server.Tick(3).Confirmations["p1"]
	if result := client.ApplyConfirmation(second); !result.Accepted {
		t.Fatalf("later confirmation should apply: %+v", result)
	}
	if result := client.ApplyConfirmation(first); result.Accepted {
		t.Fatalf("older confirmation should be ignored: %+v", result)
	}
	if result := client.ApplyConfirmation(second); result.Accepted {
		t.Fatalf("duplicate confirmation should be ignored: %+v", result)
	}
	if client.PredictedPosition() != 3 {
		t.Fatalf("rejected sequence must stay rejected while later inputs replay, got %d", client.PredictedPosition())
	}
	if result := client.ApplyConfirmation(third); !result.Accepted {
		t.Fatalf("final confirmation should apply: %+v", result)
	}
	if sequences := client.UnconfirmedSequences(); !reflect.DeepEqual(sequences, []int64{}) {
		t.Fatalf("all inputs confirmed, got %v", sequences)
	}
	if client.PredictedPosition() != third.Position {
		t.Fatalf("predicted %d want authoritative %d", client.PredictedPosition(), third.Position)
	}
	if divergence := client.Divergence(third.Position); divergence != 0 {
		t.Fatalf("final divergence = %d, want 0", divergence)
	}
}

func TestClientDuplicateReceiptAndGap(t *testing.T) {
	client, _ := NewClient(Config{WorldWidth: 10, Quota: 1, MaxStep: 5, Backlog: 10}, "p1", 0)
	first := client.AddInput(Move{Sequence: 1, Delta: 1})
	duplicate := client.AddInput(Move{Sequence: 1, Delta: 1})
	if duplicate.Existing == nil || duplicate.Existing.Accepted != first.Accepted {
		t.Fatalf("first=%+v duplicate=%+v", first, duplicate)
	}
	if result := client.AddInput(Move{Sequence: 3, Delta: 1}); result.Reason != RejectionGap {
		t.Fatalf("expected gap, got %+v", result)
	}
}

func TestDuplicateReceiptSurvivesConfirmation(t *testing.T) {
	config := Config{WorldWidth: 10, Quota: 1, MaxStep: 5, Backlog: 1}
	client, _ := NewClient(config, "p1", 0)
	move := Move{Sequence: 1, Delta: 1}
	first := client.AddInput(move)
	client.ApplyConfirmation(Confirmation{ProcessedSequence: 1, Position: 1, RejectedSequences: map[int64]struct{}{}})
	duplicate := client.AddInput(move)
	if duplicate.Existing == nil || duplicate.Existing.Accepted != first.Accepted {
		t.Fatalf("processed duplicate should return original receipt: first=%+v duplicate=%+v", first, duplicate)
	}
}
