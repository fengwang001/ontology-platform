package netcode

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type naivePlayer struct {
	position          int64
	receivedSequence  int64
	processedSequence int64
	pending           []Move
	receipts          map[int64]ReceiveResult
}

type naiveClient struct {
	position          int64
	receivedSequence  int64
	processedSequence int64
	pending           []Move
	receipts          map[int64]ReceiveResult
}

type naiveWorld struct {
	width    int64
	quota    int
	maxStep  int64
	backlog  int
	lastTick int64
	hasTick  bool
	players  map[string]*naivePlayer
	clients  map[string]*naiveClient
}

func newNaiveWorld(config Config) *naiveWorld {
	return &naiveWorld{
		width:   config.WorldWidth,
		quota:   config.Quota,
		maxStep: config.MaxStep,
		backlog: config.Backlog,
		players: make(map[string]*naivePlayer),
		clients: make(map[string]*naiveClient),
	}
}

func naiveApply(position int64, delta int64, width int64, maxStep int64) (int64, bool) {
	if delta == 0 || delta > maxStep || delta < -maxStep {
		return position, false
	}
	position += delta
	if position < 0 {
		position = 0
	}
	if position > width {
		position = width
	}
	return position, true
}

func (world *naiveWorld) register(identifier string, position int64) {
	world.players[identifier] = &naivePlayer{
		position: naiveClamp(position, world.width),
		receipts: make(map[int64]ReceiveResult),
	}
	world.clients[identifier] = &naiveClient{
		position: naiveClamp(position, world.width),
		receipts: make(map[int64]ReceiveResult),
	}
}

func naiveClamp(value int64, width int64) int64 {
	if value < 0 {
		return 0
	}
	if value > width {
		return width
	}
	return value
}

func (world *naiveWorld) submit(identifier string, move Move) ReceiveResult {
	player := world.players[identifier]
	if identifier == "" || player == nil || move.Sequence < 1 || move.Delta == 0 {
		return ReceiveResult{Reason: RejectionInvalidArgument}
	}
	if move.Sequence <= player.receivedSequence {
		previous := player.receipts[move.Sequence]
		copyOfPrevious := previous
		return ReceiveResult{Accepted: previous.Accepted, Reason: previous.Reason, Existing: &copyOfPrevious}
	}
	if move.Sequence > player.receivedSequence+1 {
		return ReceiveResult{Reason: RejectionGap}
	}
	if len(player.pending) >= world.backlog {
		return ReceiveResult{Reason: RejectionBacklogFull}
	}
	player.receivedSequence++
	player.pending = append(player.pending, move)
	result := ReceiveResult{Accepted: true}
	player.receipts[move.Sequence] = result
	return result
}

func (world *naiveWorld) addClientInput(identifier string, move Move) ReceiveResult {
	client := world.clients[identifier]
	if move.Sequence < 1 || move.Delta == 0 {
		return ReceiveResult{Reason: RejectionInvalidArgument}
	}
	if move.Sequence <= client.receivedSequence {
		previous := client.receipts[move.Sequence]
		copyOfPrevious := previous
		return ReceiveResult{Accepted: previous.Accepted, Reason: previous.Reason, Existing: &copyOfPrevious}
	}
	if move.Sequence > client.receivedSequence+1 {
		return ReceiveResult{Reason: RejectionGap}
	}
	client.receivedSequence++
	client.pending = append(client.pending, move)
	client.position, _ = naiveApply(client.position, move.Delta, world.width, world.maxStep)
	result := ReceiveResult{Accepted: true}
	client.receipts[move.Sequence] = result
	return result
}

func (world *naiveWorld) tick(now int64) (TickResult, map[string]Confirmation) {
	result := TickResult{Now: now, Accepted: true, Confirmations: map[string]Confirmation{}, Players: []string{}}
	confirmations := make(map[string]Confirmation)
	if world.hasTick && now <= world.lastTick {
		return TickResult{Now: now, Reason: RejectionClockRewound, Confirmations: map[string]Confirmation{}, Players: []string{}}, nil
	}
	world.lastTick = now
	world.hasTick = true
	identifiers := make([]string, 0, len(world.players))
	for identifier := range world.players {
		identifiers = append(identifiers, identifier)
	}
	sortStrings(identifiers)
	for _, identifier := range identifiers {
		player := world.players[identifier]
		if len(player.pending) == 0 {
			continue
		}
		result.Players = append(result.Players, identifier)
		rejected := map[int64]struct{}{}
		count := world.quota
		if count > len(player.pending) {
			count = len(player.pending)
		}
		for _, move := range player.pending[:count] {
			nextPosition, accepted := naiveApply(player.position, move.Delta, world.width, world.maxStep)
			player.position = nextPosition
			player.processedSequence = move.Sequence
			if !accepted {
				rejected[move.Sequence] = struct{}{}
			}
		}
		player.pending = append([]Move(nil), player.pending[count:]...)
		confirmation := Confirmation{ProcessedSequence: player.processedSequence, Position: player.position, RejectedSequences: rejected}
		result.Confirmations[identifier] = confirmation
		confirmations[identifier] = cloneConfirmation(confirmation)
	}
	return result, confirmations
}

func sortStrings(values []string) {
	for index := 1; index < len(values); index++ {
		for next := index; next > 0 && values[next-1] > values[next]; next-- {
			values[next-1], values[next] = values[next], values[next-1]
		}
	}
}

func cloneConfirmation(confirmation Confirmation) Confirmation {
	clone := Confirmation{
		ProcessedSequence: confirmation.ProcessedSequence,
		Position:          confirmation.Position,
		RejectedSequences: make(map[int64]struct{}, len(confirmation.RejectedSequences)),
	}
	for sequence := range confirmation.RejectedSequences {
		clone.RejectedSequences[sequence] = struct{}{}
	}
	return clone
}

func (world *naiveWorld) applyConfirmation(identifier string, confirmation Confirmation) ReconcileResult {
	client := world.clients[identifier]
	if confirmation.ProcessedSequence <= client.processedSequence {
		return ReconcileResult{Position: client.position}
	}
	client.processedSequence = confirmation.ProcessedSequence
	position := naiveClamp(confirmation.Position, world.width)
	remaining := make([]Move, 0)
	receipts := make(map[int64]ReceiveResult)
	for _, move := range client.pending {
		if move.Sequence <= confirmation.ProcessedSequence {
			continue
		}
		if _, rejected := confirmation.RejectedSequences[move.Sequence]; rejected {
			continue
		}
		remaining = append(remaining, move)
		receipts[move.Sequence] = client.receipts[move.Sequence]
		position, _ = naiveApply(position, move.Delta, world.width, world.maxStep)
	}
	client.pending = remaining
	client.receipts = receipts
	client.position = position
	return ReconcileResult{Accepted: true, Position: position}
}

func (world *naiveWorld) sequences(identifier string) []int64 {
	client := world.clients[identifier]
	result := make([]int64, len(client.pending))
	for index, move := range client.pending {
		result[index] = move.Sequence
	}
	return result
}

func TestRandomInterleavingsAgainstNaiveModel(t *testing.T) {
	const iterations = 1500
	for iteration := 0; iteration < iterations; iteration++ {
		iteration := iteration
		t.Run(fmt.Sprintf("case-%04d", iteration), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(2026100600 + iteration)))
			config := Config{
				WorldWidth: int64(rng.Intn(20) + 1),
				Quota:      rng.Intn(5) + 1,
				MaxStep:    int64(rng.Intn(5) + 1),
				Backlog:    rng.Intn(6) + 1,
			}
			if config.MaxStep > config.WorldWidth {
				config.MaxStep = config.WorldWidth
			}
			server, err := NewServer(config)
			if err != nil {
				t.Fatal(err)
			}
			model := newNaiveWorld(config)
			playerCount := rng.Intn(3) + 1
			identifiers := make([]string, 0, playerCount)
			serverClients := make(map[string]*Client, playerCount)
			for index := 0; index < playerCount; index++ {
				identifier := fmt.Sprintf("p%d", index)
				initial := int64(rng.Intn(int(config.WorldWidth) + 1))
				if err := server.RegisterPlayer(identifier, initial); err != nil {
					t.Fatal(err)
				}
				serverClient, err := NewClient(config, identifier, initial)
				if err != nil {
					t.Fatal(err)
				}
				serverClients[identifier] = serverClient
				model.register(identifier, initial)
				identifiers = append(identifiers, identifier)
			}

			now := int64(1)
			eventCount := rng.Intn(70) + 40
			log := make([]string, 0, eventCount)
			locallySent := make(map[string]struct{})
			unsentMoves := make(map[string][]Move)
			queuedConfirmations := make([]struct {
				player       string
				confirmation Confirmation
			}, 0)
			for event := 0; event < eventCount; event++ {
				identifier := identifiers[rng.Intn(len(identifiers))]
				serverPlayer := model.players[identifier]
				nextSequence := serverPlayer.receivedSequence + 1
				delta := int64(rng.Intn(int(config.MaxStep)*2+3)) - int64(config.MaxStep) - 1
				switch rng.Intn(10) {
				case 0:
					tickNow := now
					if rng.Intn(4) == 0 {
						tickNow = now - int64(rng.Intn(3)+1)
					}
					actual := server.Tick(tickNow)
					expected, produced := model.tick(tickNow)
					if tickNow >= now {
						now = tickNow + 1
					}
					if !reflect.DeepEqual(actual.Players, expected.Players) || actual.Accepted != expected.Accepted || actual.Reason != expected.Reason {
						t.Fatalf("tick mismatch actual=%+v expected=%+v log=%v", actual, expected, log)
					}
					for player, confirmation := range produced {
						queuedConfirmations = append(queuedConfirmations, struct {
							player       string
							confirmation Confirmation
						}{player, confirmation})
					}
					log = append(log, fmt.Sprintf("tick(now=%d, accepted=%v, players=%v)", tickNow, actual.Accepted, actual.Players))
				case 1:
					if len(queuedConfirmations) == 0 {
						continue
					}
					index := rng.Intn(len(queuedConfirmations))
					delivery := queuedConfirmations[index]
					actualClient := serverClients[delivery.player].ApplyConfirmation(delivery.confirmation)
					expectedClient := model.applyConfirmation(delivery.player, delivery.confirmation)
					if actualClient != expectedClient {
						t.Fatalf("client ack mismatch player=%s actual=%+v expected=%+v", delivery.player, actualClient, expectedClient)
					}
					if rng.Intn(2) == 0 {
						duplicateActual := serverClients[delivery.player].ApplyConfirmation(delivery.confirmation)
						duplicateExpected := model.applyConfirmation(delivery.player, delivery.confirmation)
						if duplicateActual != duplicateExpected {
							t.Fatalf("duplicate ack mismatch player=%s actual=%+v expected=%+v", delivery.player, duplicateActual, duplicateExpected)
						}
					}
					log = append(log, fmt.Sprintf("client-ack(player=%s, seq=%d)", delivery.player, delivery.confirmation.ProcessedSequence))
				default:
					move := Move{Sequence: nextSequence, Delta: delta}
					choice := rng.Intn(10)
					if len(unsentMoves[identifier]) > 0 {
						move = unsentMoves[identifier][0]
					} else if choice == 0 {
						move.Delta = 0
					} else if choice == 1 && serverPlayer.receivedSequence > 0 {
						move.Sequence = rng.Int63n(serverPlayer.receivedSequence) + 1
					}
					actual := server.Submit(identifier, move)
					expected := model.submit(identifier, move)
					actual.Existing = normalizeExisting(actual.Existing)
					expected.Existing = normalizeExisting(expected.Existing)
					if !reflect.DeepEqual(actual, expected) {
						t.Fatalf("submit mismatch player=%s move=%+v actual=%+v expected=%+v log=%v", identifier, move, actual, expected, log)
					}
					locallySentKey := fmt.Sprintf("%s:%d", identifier, move.Sequence)
					_, alreadyPredicted := locallySent[locallySentKey]
					shouldPredictLocally := (actual.Accepted || actual.Reason == RejectionBacklogFull) && actual.Existing == nil && !alreadyPredicted
					if shouldPredictLocally {
						locallySent[locallySentKey] = struct{}{}
						actualClientReceipt := serverClients[identifier].AddInput(move)
						expectedClientReceipt := model.addClientInput(identifier, move)
						actualClientReceipt.Existing = normalizeExisting(actualClientReceipt.Existing)
						expectedClientReceipt.Existing = normalizeExisting(expectedClientReceipt.Existing)
						if !reflect.DeepEqual(actualClientReceipt, expectedClientReceipt) {
							t.Fatalf("client submit mismatch player=%s move=%+v actual=%+v expected=%+v", identifier, move, actualClientReceipt, expectedClientReceipt)
						}
						if actual.Reason == RejectionBacklogFull {
							unsentMoves[identifier] = append(unsentMoves[identifier], move)
						}
					}
					if actual.Accepted && len(unsentMoves[identifier]) > 0 && unsentMoves[identifier][0].Sequence == move.Sequence {
						unsentMoves[identifier] = unsentMoves[identifier][1:]
					}
					log = append(log, fmt.Sprintf("submit(player=%s, move=%+v, accepted=%v, reason=%s)", identifier, move, actual.Accepted, actual.Reason))
				}
			}

			for {
				for _, identifier := range identifiers {
					if len(unsentMoves[identifier]) == 0 {
						continue
					}
					move := unsentMoves[identifier][0]
					actualReceipt := server.Submit(identifier, move)
					expectedReceipt := model.submit(identifier, move)
					actualReceipt.Existing = normalizeExisting(actualReceipt.Existing)
					expectedReceipt.Existing = normalizeExisting(expectedReceipt.Existing)
					if !reflect.DeepEqual(actualReceipt, expectedReceipt) {
						t.Fatalf("drain submit mismatch player=%s move=%+v actual=%+v expected=%+v", identifier, move, actualReceipt, expectedReceipt)
					}
					if actualReceipt.Accepted {
						unsentMoves[identifier] = unsentMoves[identifier][1:]
					}
				}
				pending := false
				for _, identifier := range identifiers {
					modelPlayer := model.players[identifier]
					if modelPlayer.processedSequence < modelPlayer.receivedSequence || len(unsentMoves[identifier]) > 0 {
						pending = true
					}
				}
				if !pending {
					break
				}
				actualTick := server.Tick(now)
				expectedTick, produced := model.tick(now)
				now++
				if !reflect.DeepEqual(actualTick.Players, expectedTick.Players) {
					t.Fatalf("drain tick mismatch actual=%+v expected=%+v", actualTick, expectedTick)
				}
				for player, confirmation := range produced {
					queuedConfirmations = append(queuedConfirmations, struct {
						player       string
						confirmation Confirmation
					}{player, confirmation})
				}
			}
			rng.Shuffle(len(queuedConfirmations), func(left, right int) {
				queuedConfirmations[left], queuedConfirmations[right] = queuedConfirmations[right], queuedConfirmations[left]
			})
			for _, delivery := range queuedConfirmations {
				actual := serverClients[delivery.player].ApplyConfirmation(delivery.confirmation)
				expected := model.applyConfirmation(delivery.player, delivery.confirmation)
				if actual != expected {
					t.Fatalf("drain ack mismatch player=%s actual=%+v expected=%+v", delivery.player, actual, expected)
				}
			}
			for _, identifier := range identifiers {
				serverPosition, exists := server.Position(identifier)
				if !exists {
					t.Fatalf("missing server position for %s", identifier)
				}
				modelPlayer := model.players[identifier]
				if serverPosition != modelPlayer.position {
					t.Fatalf("server position mismatch player=%s actual=%d expected=%d", identifier, serverPosition, modelPlayer.position)
				}
				serverProcessed, _ := server.ProcessedSequence(identifier)
				if serverProcessed != modelPlayer.processedSequence {
					t.Fatalf("processed mismatch player=%s actual=%d expected=%d", identifier, serverProcessed, modelPlayer.processedSequence)
				}
				predicted := serverClients[identifier].PredictedPosition()
				if predicted != model.clients[identifier].position || predicted != serverPosition {
					t.Fatalf("final position mismatch player=%s actual=%d model=%d server=%d", identifier, predicted, model.clients[identifier].position, serverPosition)
				}
				if divergence := serverClients[identifier].Divergence(serverPosition); divergence != 0 {
					t.Fatalf("final divergence player=%s divergence=%d", identifier, divergence)
				}
				if sequences := serverClients[identifier].UnconfirmedSequences(); len(sequences) != 0 {
					t.Fatalf("unconfirmed sequences remain player=%s sequences=%v", identifier, sequences)
				}
			}
			t.Logf("inputs, outputs and decisions: %v", log)
		})
	}
}

func normalizeExisting(result *ReceiveResult) *ReceiveResult {
	if result == nil {
		return nil
	}
	copyOfResult := *result
	copyOfResult.Existing = nil
	return &copyOfResult
}
