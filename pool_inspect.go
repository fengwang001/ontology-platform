package thinpool

func (p *Pool) Snapshot() PoolSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	volumes := make(map[string]VolumeSnapshot, len(p.volumes))
	for name, vol := range p.volumes {
		volumes[name] = VolumeSnapshot{
			Virtual:  vol.virtual,
			Reserved: vol.reserved,
			Used:     vol.used,
			Deficit:  volumeDeficit(vol),
			Physical: vol.mapping.snapshot(),
		}
	}

	return PoolSnapshot{
		PhysicalBlocks: p.physicalBlocks,
		Allocated:      p.allocatedBlocks(),
		Free:           p.freeBlocks(),
		TotalVirtual:   p.totalVirtual,
		TotalReserved:  p.totalReserved,
		TotalDeficit:   p.totalDeficit,
		Level:          p.level,
		Volumes:        volumes,
	}
}

func (p *Pool) Events() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()

	events := make([]Event, len(p.events))
	copy(events, p.events)
	return events
}
