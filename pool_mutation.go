package thinpool

func (p *Pool) WriteBlock(name string, virtualBlock uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if name == "" {
		return thinError(ErrInvalidArgument, "volume name is empty")
	}
	vol := p.volumes[name]
	if vol == nil {
		return thinError(ErrNotFound, "volume not found")
	}
	if virtualBlock >= vol.virtual {
		return thinError(ErrInvalidArgument, "virtual block is outside volume")
	}

	if _, mapped := vol.mapping.lookup(virtualBlock); mapped {
		return nil
	}
	remainingDeficit := p.totalDeficit - volumeDeficit(vol)
	if p.freeBlocks() <= remainingDeficit {
		return thinError(ErrPoolExhausted, "allocation would consume another volume reservation")
	}

	physicalBlock, ok := p.allocator.allocate()
	if !ok {
		return thinError(ErrPoolExhausted, "pool has no free physical blocks")
	}

	oldDeficit := volumeDeficit(vol)
	vol.mapping.insert(virtualBlock, physicalBlock)
	vol.used++
	newDeficit := volumeDeficit(vol)
	p.totalDeficit -= oldDeficit - newDeficit
	p.appendWatermarkEvent()
	return nil
}

func (p *Pool) ReclaimRange(name string, start, length uint64) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if name == "" {
		return 0, thinError(ErrInvalidArgument, "volume name is empty")
	}
	vol := p.volumes[name]
	if vol == nil {
		return 0, thinError(ErrNotFound, "volume not found")
	}
	if length == 0 {
		return 0, nil
	}
	if overflowAdd(start, length) || start+length > vol.virtual {
		return 0, thinError(ErrInvalidArgument, "reclaim range is outside volume")
	}

	freedBlocks := vol.mapping.deleteRange(start, start+length)
	freedCount := uint64(len(freedBlocks))
	if freedCount > 0 {
		oldDeficit := volumeDeficit(vol)
		for _, physicalBlock := range freedBlocks {
			p.allocator.free(physicalBlock)
		}
		vol.used -= freedCount
		newDeficit := volumeDeficit(vol)
		p.totalDeficit += newDeficit - oldDeficit
		p.appendWatermarkEvent()
	}
	return freedCount, nil
}

func (p *Pool) DeleteVolume(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if name == "" {
		return thinError(ErrInvalidArgument, "volume name is empty")
	}
	vol := p.volumes[name]
	if vol == nil {
		return thinError(ErrNotFound, "volume not found")
	}

	freedBlocks := vol.mapping.deleteRange(0, vol.virtual)
	for _, physicalBlock := range freedBlocks {
		p.allocator.free(physicalBlock)
	}
	p.totalVirtual -= vol.virtual
	p.totalReserved -= vol.reserved
	p.totalDeficit -= volumeDeficit(vol)
	delete(p.volumes, name)
	p.appendWatermarkEvent()
	return nil
}
