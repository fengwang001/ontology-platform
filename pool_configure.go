package thinpool

func (p *Pool) ResizeVolume(name string, newVirtualBlocks uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if name == "" {
		return thinError(ErrInvalidArgument, "volume name is empty")
	}
	vol := p.volumes[name]
	if vol == nil {
		return thinError(ErrNotFound, "volume not found")
	}

	if newVirtualBlocks >= vol.virtual {
		added := newVirtualBlocks - vol.virtual
		if overflowAdd(p.totalVirtual, added) {
			return thinError(ErrOvercommit, "overcommit limit exceeded")
		}
		limit, fits := p.virtualLimit()
		if !fits || p.totalVirtual+added > limit {
			return thinError(ErrOvercommit, "overcommit limit exceeded")
		}
	}

	if vol.reserved > newVirtualBlocks {
		return thinError(ErrReserveVolume, "reservation exceeds new volume size")
	}
	if newVirtualBlocks < vol.virtual && vol.mapping.countRange(newVirtualBlocks, vol.virtual) > 0 {
		return thinError(ErrVolumeHasData, "mapped blocks remain in removed range")
	}

	if newVirtualBlocks < vol.virtual {
		p.totalVirtual -= vol.virtual - newVirtualBlocks
	} else {
		p.totalVirtual += newVirtualBlocks - vol.virtual
	}
	vol.virtual = newVirtualBlocks
	return nil
}

func (p *Pool) SetReservation(name string, reservedBlocks uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if name == "" {
		return thinError(ErrInvalidArgument, "volume name is empty")
	}
	vol := p.volumes[name]
	if vol == nil {
		return thinError(ErrNotFound, "volume not found")
	}
	newTotalReserved := p.totalReserved - vol.reserved
	if overflowAdd(newTotalReserved, reservedBlocks) || newTotalReserved+reservedBlocks > p.physicalBlocks {
		return thinError(ErrReservePool, "total reservation exceeds pool size")
	}
	if reservedBlocks > vol.virtual {
		return thinError(ErrReserveVolume, "reservation exceeds volume size")
	}

	oldDeficit := volumeDeficit(vol)
	newDeficit := reservedBlocks
	if reservedBlocks > vol.used {
		newDeficit = reservedBlocks - vol.used
	} else {
		newDeficit = 0
	}
	newTotalDeficit := p.totalDeficit - oldDeficit + newDeficit
	if newTotalDeficit > p.freeBlocks() {
		return thinError(ErrInsufficient, "not enough free blocks for reservation")
	}

	p.totalReserved = newTotalReserved + reservedBlocks
	p.totalDeficit = newTotalDeficit
	vol.reserved = reservedBlocks
	return nil
}
