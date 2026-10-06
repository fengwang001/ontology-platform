package thinpool

import (
	"math"
	"math/bits"
	"sync"
)

type Pool struct {
	mu              sync.Mutex
	physicalBlocks  uint64
	overcommit      uint64
	warningPercent  uint64
	criticalPercent uint64
	allocator       *physicalAllocator
	volumes         map[string]*volume
	totalVirtual    uint64
	totalReserved   uint64
	totalDeficit    uint64
	level           WaterLevel
	events          []Event
	nextEventNumber uint64
}

type PoolSnapshot struct {
	PhysicalBlocks uint64
	Allocated      uint64
	Free           uint64
	TotalVirtual   uint64
	TotalReserved  uint64
	TotalDeficit   uint64
	Level          WaterLevel
	Volumes        map[string]VolumeSnapshot
}

func NewPool(physicalBlocks, overcommitPercent, warningPercent, criticalPercent uint64) (*Pool, error) {
	if physicalBlocks == 0 || warningPercent > criticalPercent {
		return nil, thinError(ErrInvalidArgument, "invalid pool parameters")
	}

	pool := &Pool{
		physicalBlocks:  physicalBlocks,
		overcommit:      overcommitPercent,
		warningPercent:  warningPercent,
		criticalPercent: criticalPercent,
		allocator:       newPhysicalAllocator(physicalBlocks),
		volumes:         make(map[string]*volume),
	}
	pool.level = waterLevel(0, physicalBlocks, warningPercent, criticalPercent)
	return pool, nil
}

func (p *Pool) allocatedBlocks() uint64 {
	return p.allocator.allocated
}

func (p *Pool) freeBlocks() uint64 {
	return p.physicalBlocks - p.allocatedBlocks()
}

func (p *Pool) virtualLimit() (uint64, bool) {
	high, low := bits.Mul64(p.physicalBlocks, p.overcommit)
	if high >= 100 {
		return math.MaxUint64, true
	}
	quotient, _ := bits.Div64(high, low, 100)
	return quotient, true
}

func volumeDeficit(vol *volume) uint64 {
	if vol.reserved > vol.used {
		return vol.reserved - vol.used
	}
	return 0
}

func (p *Pool) appendWatermarkEvent() {
	allocated := p.allocatedBlocks()
	newLevel := waterLevel(allocated, p.physicalBlocks, p.warningPercent, p.criticalPercent)
	if newLevel == p.level {
		return
	}
	p.nextEventNumber++
	p.events = append(p.events, Event{
		Sequence:  p.nextEventNumber,
		OldLevel:  p.level,
		NewLevel:  newLevel,
		Allocated: allocated,
	})
	p.level = newLevel
}

func (p *Pool) CreateVolume(name string, virtualBlocks, reservedBlocks uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if name == "" {
		return thinError(ErrInvalidArgument, "invalid volume parameters")
	}
	if _, exists := p.volumes[name]; exists {
		return thinError(ErrExists, "volume already exists")
	}

	if overflowAdd(p.totalVirtual, virtualBlocks) {
		return thinError(ErrOvercommit, "overcommit limit exceeded")
	}
	limit, fits := p.virtualLimit()
	if !fits || p.totalVirtual+virtualBlocks > limit {
		return thinError(ErrOvercommit, "overcommit limit exceeded")
	}
	if overflowAdd(p.totalReserved, reservedBlocks) || p.totalReserved+reservedBlocks > p.physicalBlocks {
		return thinError(ErrReservePool, "total reservation exceeds pool size")
	}
	if reservedBlocks > virtualBlocks {
		return thinError(ErrReserveVolume, "reservation exceeds volume size")
	}
	if reservedBlocks > p.freeBlocks()-p.totalDeficit {
		return thinError(ErrInsufficient, "not enough uncommitted free blocks")
	}

	vol := &volume{
		name:     name,
		virtual:  virtualBlocks,
		reserved: reservedBlocks,
		mapping:  newBlockMapping(),
	}
	p.volumes[name] = vol
	p.totalVirtual += virtualBlocks
	p.totalReserved += reservedBlocks
	p.totalDeficit += reservedBlocks
	return nil
}

func overflowAdd(a, b uint64) bool {
	return a+b < a
}
