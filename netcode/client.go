package netcode

import (
	"sort"
	"sync"
)

type clientInput struct {
	move    Move
	receipt ReceiveResult
}

type Client struct {
	config            Config
	playerIdentifier  string
	mu                sync.Mutex
	predictedPosition int64
	receivedSequence  int64
	processedSequence int64
	pending           []clientInput
	receipts          map[int64]ReceiveResult
	processedReceipts map[int64]ReceiveResult
}

func NewClient(config Config, playerIdentifier string, initialPosition int64) (*Client, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if playerIdentifier == "" {
		return nil, ErrInvalidConfig
	}
	return &Client{
		config:            config,
		playerIdentifier:  playerIdentifier,
		predictedPosition: clampPosition(initialPosition, config.WorldWidth),
		receipts:          make(map[int64]ReceiveResult),
		processedReceipts: make(map[int64]ReceiveResult),
	}, nil
}

func (client *Client) AddInput(move Move) ReceiveResult {
	if move.Sequence < 1 || move.Delta == 0 {
		return ReceiveResult{Reason: RejectionInvalidArgument}
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if move.Sequence <= client.receivedSequence {
		previous := client.receiptForSequence(move.Sequence)
		return ReceiveResult{
			Accepted: previous.Accepted,
			Reason:   previous.Reason,
			Existing: pointerToReceipt(previous),
		}
	}
	if move.Sequence > client.receivedSequence+1 {
		return ReceiveResult{Reason: RejectionGap}
	}
	client.receivedSequence++
	result := ReceiveResult{Accepted: true}
	client.pending = append(client.pending, clientInput{move: move, receipt: result})
	client.predictedPosition, _ = applyMove(
		client.predictedPosition,
		move.Delta,
		client.config.WorldWidth,
		client.config.MaxStep,
	)
	client.receipts[move.Sequence] = result
	return result
}

func (client *Client) ApplyConfirmation(confirmation Confirmation) ReconcileResult {
	if confirmation.ProcessedSequence <= 0 {
		client.mu.Lock()
		defer client.mu.Unlock()
		return ReconcileResult{Accepted: false, Position: client.predictedPosition}
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if confirmation.ProcessedSequence <= client.processedSequence {
		return ReconcileResult{Accepted: false, Position: client.predictedPosition}
	}
	client.processedSequence = confirmation.ProcessedSequence
	position := clampPosition(confirmation.Position, client.config.WorldWidth)
	firstRemaining := sort.Search(len(client.pending), func(index int) bool {
		return client.pending[index].move.Sequence > confirmation.ProcessedSequence
	})
	remaining := make([]clientInput, 0, len(client.pending)-firstRemaining)
	retainedReceipts := make(map[int64]ReceiveResult, len(client.pending)-firstRemaining)
	for _, input := range client.pending[firstRemaining:] {
		remaining = append(remaining, input)
		retainedReceipts[input.move.Sequence] = input.receipt
		position, _ = applyMove(position, input.move.Delta, client.config.WorldWidth, client.config.MaxStep)
	}
	for _, input := range client.pending[:firstRemaining] {
		client.processedReceipts[input.move.Sequence] = input.receipt
	}
	client.pending = remaining
	client.receipts = retainedReceipts
	client.predictedPosition = position
	return ReconcileResult{Accepted: true, Position: position}
}

func (client *Client) PredictedPosition() int64 {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.predictedPosition
}

func (client *Client) UnconfirmedSequences() []int64 {
	client.mu.Lock()
	defer client.mu.Unlock()
	result := make([]int64, len(client.pending))
	for index, input := range client.pending {
		result[index] = input.move.Sequence
	}
	return result
}

func (client *Client) LastProcessedSequence() int64 {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.processedSequence
}

func (client *Client) Divergence(serverPosition int64) int64 {
	client.mu.Lock()
	defer client.mu.Unlock()
	serverPosition = clampPosition(serverPosition, client.config.WorldWidth)
	return client.predictedPosition - serverPosition
}

func (client *Client) receiptForSequence(sequence int64) ReceiveResult {
	if sequence < 1 || sequence > client.receivedSequence {
		return ReceiveResult{}
	}
	if receipt, exists := client.processedReceipts[sequence]; exists {
		return receipt
	}
	return client.receipts[sequence]
}

func pointerToReceipt(result ReceiveResult) *ReceiveResult {
	copyOfResult := result
	return &copyOfResult
}
