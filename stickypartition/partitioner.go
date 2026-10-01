package stickypartition

import (
	"errors"
	"sync"
)

var (
	ErrInvalidPartitionCount = errors.New("partition count must be at least 1")
	ErrInvalidThreshold      = errors.New("batch threshold must be at least 1 byte")
	ErrInvalidMessageSize    = errors.New("message size must be at least 1 byte")
	ErrPartitionOutOfRange   = errors.New("partition number is out of range")
	ErrNoAvailablePartition  = errors.New("no partition is available")
)

type Snapshot struct {
	StickyPartition  int
	AccumulatedBytes int
	Available        []bool
}

type StickyPartitioner struct {
	mu         sync.Mutex
	partitions int
	threshold  int
	sticky     int
	bytes      int
	available  []bool
}

func New(partitions, thresholdBytes int) (*StickyPartitioner, error) {
	if partitions < 1 {
		return nil, ErrInvalidPartitionCount
	}
	if thresholdBytes < 1 {
		return nil, ErrInvalidThreshold
	}

	p := &StickyPartitioner{
		partitions: partitions,
		threshold:  thresholdBytes,
		available:  make([]bool, partitions),
	}
	for i := range p.available {
		p.available[i] = true
	}
	return p, nil
}

func (p *StickyPartitioner) SendWithKey(key []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	hash := fnv1a32(key)
	return int(hash % uint32(p.partitions)), nil
}

func (p *StickyPartitioner) SendKeyless(size int) (int, error) {
	if size < 1 {
		return 0, ErrInvalidMessageSize
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.available[p.sticky] {
		next, ok := p.nextAvailableFrom(p.sticky)
		if !ok {
			return 0, ErrNoAvailablePartition
		}
		p.sticky = next
		p.bytes = 0
	}

	partition := p.sticky
	p.bytes += size

	if p.bytes >= p.threshold {
		if next, ok := p.nextAvailableFrom(p.sticky); ok {
			p.sticky = next
		}
		p.bytes = 0
	}

	return partition, nil
}

func (p *StickyPartitioner) SetAvailable(partition int, available bool) error {
	if partition < 0 || partition >= p.partitions {
		return ErrPartitionOutOfRange
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.available[partition] = available
	return nil
}

func (p *StickyPartitioner) SnapshotState() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	available := append([]bool(nil), p.available...)
	return Snapshot{
		StickyPartition:  p.sticky,
		AccumulatedBytes: p.bytes,
		Available:        available,
	}
}

func (p *StickyPartitioner) nextAvailableFrom(current int) (int, bool) {
	for offset := 1; offset <= p.partitions; offset++ {
		partition := (current + offset) % p.partitions
		if p.available[partition] {
			return partition, true
		}
	}
	return 0, false
}

func fnv1a32(data []byte) uint32 {
	const (
		offsetBasis uint32 = 2166136261
		prime       uint32 = 16777619
	)

	hash := offsetBasis
	for _, b := range data {
		hash ^= uint32(b)
		hash *= prime
	}
	return hash
}
