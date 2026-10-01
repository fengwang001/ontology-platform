package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentOperations(t *testing.T) {
	registry := NewRegistry()
	connectStart := make(chan struct{})
	const clientCount = 200
	var connectors sync.WaitGroup

	for i := 0; i < clientCount; i++ {
		clientID := fmt.Sprintf("client-%03d", i)
		connectors.Add(1)

		go func() {
			defer connectors.Done()
			<-connectStart
			_ = registry.Connect(clientID, 0, &Will{
				Topic:       "will/" + clientID,
				Payload:     []byte(clientID),
				DelayMillis: 5,
			}, 0)
		}()
	}
	close(connectStart)
	connectors.Wait()

	operationStart := make(chan struct{})
	var operations sync.WaitGroup
	startReaders := make(chan struct{})
	var readers sync.WaitGroup
	stopReaders := make(chan struct{})
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-startReaders
			for {
				select {
				case <-stopReaders:
					return
				default:
					_ = registry.Publications()
				}
			}
		}()
	}
	close(startReaders)

	for i := 0; i < clientCount; i++ {
		clientID := fmt.Sprintf("client-%03d", i)
		operations.Add(2)

		go func() {
			defer operations.Done()
			<-operationStart
			_ = registry.Activity(clientID, 10)
		}()

		go func() {
			defer operations.Done()
			<-operationStart
			_ = registry.Disconnect(clientID, false, 20)
		}()
	}

	close(operationStart)
	operations.Wait()
	close(stopReaders)
	readers.Wait()

	published, err := registry.Advance(100)
	if err != nil {
		t.Fatalf("Advance() error = %v", err)
	}
	if len(published) != clientCount {
		t.Fatalf("concurrent publication count = %d, want %d", len(published), clientCount)
	}

}
