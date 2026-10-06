package netcode

import (
	"sync"
	"testing"
)

func TestConcurrentSubmitsQueriesAndConfirmations(t *testing.T) {
	config := Config{WorldWidth: 100, Quota: 10, MaxStep: 10, Backlog: 100}
	server, err := NewServer(config)
	if err != nil {
		t.Fatal(err)
	}
	const players = 8
	clients := make([]*Client, players)
	for index := range clients {
		identifier := "p" + itoa(index)
		if err := server.RegisterPlayer(identifier, 0); err != nil {
			t.Fatal(err)
		}
		clients[index], err = NewClient(config, identifier, 0)
		if err != nil {
			t.Fatal(err)
		}
	}

	var waitGroup sync.WaitGroup
	for worker := 0; worker < players; worker++ {
		worker := worker
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			identifier := "p" + itoa(worker)
			client := clients[worker]
			for sequence := int64(1); sequence <= 50; sequence++ {
				move := Move{Sequence: sequence, Delta: int64(worker%5) + 1}
				if result := server.Submit(identifier, move); !result.Accepted {
					t.Errorf("submit player=%s move=%+v: %+v", identifier, move, result)
					return
				}
				if result := client.AddInput(move); !result.Accepted {
					t.Errorf("client add player=%s move=%+v: %+v", identifier, move, result)
					return
				}
			}
		}()
	}
	waitGroup.Wait()

	for now := int64(1); now <= 5; now++ {
		result := server.Tick(now)
		var confirmationWait sync.WaitGroup
		for _, identifier := range result.Players {
			confirmation := result.Confirmations[identifier]
			index := int(identifier[1] - '0')
			confirmationWait.Add(1)
			go func() {
				defer confirmationWait.Done()
				clients[index].ApplyConfirmation(confirmation)
				clients[index].PredictedPosition()
			}()
		}
		confirmationWait.Wait()
	}

	for index, client := range clients {
		identifier := "p" + itoa(index)
		serverPosition, exists := server.Position(identifier)
		if !exists {
			t.Fatalf("missing player %s", identifier)
		}
		if client.PredictedPosition() != serverPosition {
			t.Fatalf("player=%s predicted=%d server=%d", identifier, client.PredictedPosition(), serverPosition)
		}
		if len(client.UnconfirmedSequences()) != 0 {
			t.Fatalf("player=%s unconfirmed=%v", identifier, client.UnconfirmedSequences())
		}
	}
}
