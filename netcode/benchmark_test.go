package netcode

import "testing"

func BenchmarkServerTickProcessesOnlyActivePlayers(b *testing.B) {
	config := Config{WorldWidth: 1_000_000, Quota: 50, MaxStep: 1_000_000, Backlog: 50}
	server, err := NewServer(config)
	if err != nil {
		b.Fatal(err)
	}
	const idlePlayers = 1000
	for identifier := 0; identifier < idlePlayers; identifier++ {
		if err := server.RegisterPlayer("idle-"+itoa(identifier), 0); err != nil {
			b.Fatal(err)
		}
	}
	if err := server.RegisterPlayer("active", 0); err != nil {
		b.Fatal(err)
	}
	for sequence := int64(1); sequence <= 50; sequence++ {
		if result := server.Submit("active", Move{Sequence: sequence, Delta: 1}); !result.Accepted {
			b.Fatalf("submit sequence %d: %+v", sequence, result)
		}
	}
	now := int64(1)
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		server.active = append(server.active, "active")
		player := server.players["active"]
		player.active = true
		player.position = 0
		player.processedSequence = 0
		player.receivedSequence = 50
		player.pending = make([]Move, 50)
		for sequence := range player.pending {
			player.pending[sequence] = Move{Sequence: int64(sequence + 1), Delta: 1}
		}
		result := server.Tick(now)
		now++
		if len(result.Players) != 1 || result.Players[0] != "active" {
			b.Fatalf("unexpected changed players: %v", result.Players)
		}
	}
}

func BenchmarkClientApplyConfirmationReplaysOnlyRemaining(b *testing.B) {
	config := Config{WorldWidth: 1_000_000, Quota: 50, MaxStep: 1_000_000, Backlog: 4096}
	client, err := NewClient(config, "active", 0)
	if err != nil {
		b.Fatal(err)
	}
	const remaining = 128
	for sequence := int64(1); sequence <= 4096; sequence++ {
		if result := client.AddInput(Move{Sequence: sequence, Delta: 1}); !result.Accepted {
			b.Fatalf("add sequence %d: %+v", sequence, result)
		}
	}
	confirmation := Confirmation{
		ProcessedSequence: 4096 - remaining,
		Position:          4096 - remaining,
		RejectedSequences: map[int64]struct{}{},
	}
	preparedPending := append([]clientInput(nil), client.pending...)
	preparedReceipts := make(map[int64]ReceiveResult, len(client.receipts))
	for sequence, receipt := range client.receipts {
		preparedReceipts[sequence] = receipt
	}
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		client.processedSequence = 0
		client.predictedPosition = 0
		client.pending = make([]clientInput, 4096)
		copy(client.pending, preparedPending)
		client.receipts = preparedReceipts
		if result := client.ApplyConfirmation(confirmation); !result.Accepted {
			b.Fatalf("apply confirmation: %+v", result)
		}
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
