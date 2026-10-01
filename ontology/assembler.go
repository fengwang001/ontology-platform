// Package ontology provides a deterministic, concurrency-safe multi-partition
// pull-response assembler with total and per-partition byte budgets.
package ontology

import (
	"errors"
	"log"
	"sync"
)

var (
	ErrInvalidPartitions      = errors.New("number of partitions must be at least 1")
	ErrInvalidTotalBudget     = errors.New("total byte budget must be at least 1")
	ErrInvalidPartitionBudget = errors.New("partition byte budget must be at least 1")
	ErrPartitionOutOfRange    = errors.New("partition is out of range")
	ErrInvalidMessageSize     = errors.New("message size must be at least 1")
	ErrNoData                 = errors.New("no messages are available")
)

type Logger interface {
	Printf(format string, args ...any)
}

type Message[K comparable] struct {
	Partition int
	Offset    int
	Size      int
	Payload   K
}

type FetchResult[K comparable] struct {
	Messages           []Message[K]
	TotalBytes         int
	PartitionBytes     []int
	NextStartPartition int
}

type Snapshot struct {
	Partitions         int
	NextStartPartition int
	NextOffsets        []int
	AvailableCounts    []int
	AvailableBytes     []int
}

type Assembler[K comparable] struct {
	mu              sync.Mutex
	partitions      int
	totalBudget     int
	partitionBudget int
	queues          [][]storedMessage[K]
	consumed        []int
	startPartition  int
	logger          Logger
}

type storedMessage[K comparable] struct {
	size    int
	payload K
}

func New[K comparable](partitionCount int, totalBudget int, partitionBudget int, logger Logger) (*Assembler[K], error) {
	if partitionCount < 1 {
		if logger != nil {
			logger.Printf("reject New: input N=%d W=%d P=%d; reason=N<1", partitionCount, totalBudget, partitionBudget)
		}
		return nil, ErrInvalidPartitions
	}
	if totalBudget < 1 {
		if logger != nil {
			logger.Printf("reject New: input N=%d W=%d P=%d; reason=W<1", partitionCount, totalBudget, partitionBudget)
		}
		return nil, ErrInvalidTotalBudget
	}
	if partitionBudget < 1 {
		if logger != nil {
			logger.Printf("reject New: input N=%d W=%d P=%d; reason=P<1", partitionCount, totalBudget, partitionBudget)
		}
		return nil, ErrInvalidPartitionBudget
	}
	if logger == nil {
		logger = log.Default()
	}
	assembler := &Assembler[K]{
		partitions:      partitionCount,
		totalBudget:     totalBudget,
		partitionBudget: partitionBudget,
		queues:          make([][]storedMessage[K], partitionCount),
		consumed:        make([]int, partitionCount),
		logger:          logger,
	}
	logger.Printf("accept New: N=%d W=%d P=%d; initial start=0", partitionCount, totalBudget, partitionBudget)
	return assembler, nil
}

func (a *Assembler[K]) Append(partition int, size int, payload K) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if partition < 0 || partition >= a.partitions {
		a.logger.Printf("reject Append: partition=%d size=%d payload=%v; reason=partition out of range [0,%d); state unchanged", partition, size, payload, a.partitions)
		return 0, ErrPartitionOutOfRange
	}
	if size < 1 {
		a.logger.Printf("reject Append: partition=%d size=%d payload=%v; reason=message size<1; state unchanged", partition, size, payload)
		return 0, ErrInvalidMessageSize
	}

	offset := len(a.queues[partition])
	a.queues[partition] = append(a.queues[partition], storedMessage[K]{size: size, payload: payload})
	a.logger.Printf("accept Append: partition=%d offset=%d size=%d payload=%v", partition, offset, size, payload)
	return offset, nil
}

func (a *Assembler[K]) Fetch() (*FetchResult[K], error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	start := a.startPartition
	messages := make([]Message[K], 0)
	partitionBytes := make([]int, a.partitions)
	nextConsumed := make([]int, a.partitions)
	copy(nextConsumed, a.consumed)
	totalBytes := 0
	lastPartition := -1

	a.logger.Printf("start Fetch: start=%d consumed=%v W=%d P=%d", start, a.consumed, a.totalBudget, a.partitionBudget)
	for step := 0; step < a.partitions; step++ {
		partition := (start + step) % a.partitions
		cursor := nextConsumed[partition]
		for cursor < len(a.queues[partition]) {
			record := a.queues[partition][cursor]
			if totalBytes == 0 {
				messages = append(messages, Message[K]{
					Partition: partition,
					Offset:    cursor,
					Size:      record.size,
					Payload:   record.payload,
				})
				partitionBytes[partition] += record.size
				totalBytes += record.size
				cursor++
				lastPartition = partition
				a.logger.Printf("decision: partition=%d offset=%d size=%d take-first-exception total=%d partitionBytes=%d", partition, cursor-1, record.size, totalBytes, partitionBytes[partition])
				continue
			}
			if partitionBytes[partition]+record.size > a.partitionBudget {
				a.logger.Printf("decision: partition=%d offset=%d size=%d blocked-by-partition partitionBytes+size=%d P=%d; offset retained=%d", partition, cursor, record.size, partitionBytes[partition]+record.size, a.partitionBudget, cursor)
				break
			}
			if totalBytes+record.size > a.totalBudget {
				a.logger.Printf("decision: partition=%d offset=%d size=%d blocked-by-total total+size=%d W=%d; offset retained=%d", partition, cursor, record.size, totalBytes+record.size, a.totalBudget, cursor)
				break
			}
			messages = append(messages, Message[K]{
				Partition: partition,
				Offset:    cursor,
				Size:      record.size,
				Payload:   record.payload,
			})
			partitionBytes[partition] += record.size
			totalBytes += record.size
			cursor++
			lastPartition = partition
			a.logger.Printf("decision: partition=%d offset=%d size=%d take total=%d partitionBytes=%d", partition, cursor-1, record.size, totalBytes, partitionBytes[partition])
		}
		if cursor == len(a.queues[partition]) {
			a.logger.Printf("decision: partition=%d stop-at-end nextOffset=%d", partition, cursor)
		}
		nextConsumed[partition] = cursor
	}

	if len(messages) == 0 {
		a.logger.Printf("reject Fetch: start=%d consumed=%v; reason=no available data; state unchanged", start, a.consumed)
		return nil, ErrNoData
	}

	a.consumed = nextConsumed
	a.startPartition = (lastPartition + 1) % a.partitions
	result := &FetchResult[K]{
		Messages:           messages,
		TotalBytes:         totalBytes,
		PartitionBytes:     partitionBytes,
		NextStartPartition: a.startPartition,
	}
	a.logger.Printf("finish Fetch: output messages=%v totalBytes=%d partitionBytes=%v nextStart=%d", result.Messages, totalBytes, partitionBytes, a.startPartition)
	return result, nil
}

func (a *Assembler[K]) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()

	snapshot := Snapshot{
		Partitions:         a.partitions,
		NextStartPartition: a.startPartition,
		NextOffsets:        append([]int(nil), a.consumed...),
		AvailableCounts:    make([]int, a.partitions),
		AvailableBytes:     make([]int, a.partitions),
	}
	for partition := range a.queues {
		for _, record := range a.queues[partition][a.consumed[partition]:] {
			snapshot.AvailableCounts[partition]++
			snapshot.AvailableBytes[partition] += record.size
		}
	}
	a.logger.Printf("query Snapshot: output start=%d consumed=%v availableCounts=%v availableBytes=%v", snapshot.NextStartPartition, snapshot.NextOffsets, snapshot.AvailableCounts, snapshot.AvailableBytes)
	return snapshot
}
