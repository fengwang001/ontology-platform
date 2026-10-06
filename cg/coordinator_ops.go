package cg

import "fmt"

func (c *Coordinator) BeginSnapshot(t int64, groupID string, deadline int64) (uint64, error) {
	var id uint64
	var e *Error
	e0 := c.run("begin_snapshot", fmt.Sprintf("group=%q deadline=%d", groupID, deadline), t,
		func() *Error {
			if !nonEmpty(groupID) || deadline < t {
				return errf(InvalidArgument, "group id required and deadline must be >= begin time")
			}
			return nil
		},
		groupID,
		func(ctx *opCtx) {
			grp, ok := c.groups[groupID]
			if !ok {
				e = ctx.reject(errf(NotFound, "group %q not found", groupID))
				return
			}
			ctx.timeout(grp)
			if !grp.idle() {
				e = ctx.reject(errf(StateError, "snapshot already in progress"))
				return
			}
			id = c.nextSnap
			c.nextSnap++
			grp.begin(id, deadline)
			ctx.ok(fmt.Sprintf("snapshot=%d", id))
		})
	if e0 != nil {
		return 0, e0
	}
	if e != nil {
		return 0, e
	}
	return id, nil
}

func (c *Coordinator) ConfirmFreeze(t int64, groupID, volumeID string) (bool, error) {
	var last bool
	var e *Error
	e0 := c.run("confirm_freeze", fmt.Sprintf("group=%q volume=%q", groupID, volumeID), t,
		func() *Error {
			if !nonEmpty(groupID) || !nonEmpty(volumeID) {
				return errf(InvalidArgument, "group id and volume id required")
			}
			return nil
		},
		groupID,
		func(ctx *opCtx) {
			grp, ok := c.groups[groupID]
			if !ok {
				e = ctx.reject(errf(NotFound, "group %q not found", groupID))
				return
			}
			ctx.timeout(grp)
			if grp.snap == nil || grp.snap.phase != Freezing {
				e = ctx.reject(errf(StateError, "snapshot not in freezing phase"))
				return
			}
			if !grp.has(volumeID) {
				e = ctx.reject(errf(NotFound, "volume %q not in group %q", volumeID, groupID))
				return
			}
			if grp.snap.confirmed[volumeID] {
				e = ctx.reject(errf(DuplicateConfirm, "volume %q already confirmed", volumeID))
				return
			}
			last = grp.confirm(volumeID, t)
			if last {
				ctx.ok(fmt.Sprintf("volume=%q last=true point=%d", volumeID, t))
			} else {
				ctx.ok(fmt.Sprintf("volume=%q last=false pending=%d", volumeID, grp.snap.pending))
			}
		})
	if e0 != nil {
		return false, e0
	}
	if e != nil {
		return false, e
	}
	return last, nil
}

func (c *Coordinator) Commit(t int64, groupID string) (*SnapshotRecord, error) {
	var rec *SnapshotRecord
	var e *Error
	e0 := c.run("commit", fmt.Sprintf("group=%q", groupID), t,
		func() *Error {
			if !nonEmpty(groupID) {
				return errf(InvalidArgument, "group id required")
			}
			return nil
		},
		groupID,
		func(ctx *opCtx) {
			grp, ok := c.groups[groupID]
			if !ok {
				e = ctx.reject(errf(NotFound, "group %q not found", groupID))
				return
			}
			ctx.timeout(grp)
			if grp.snap == nil || grp.snap.phase != Frozen {
				e = ctx.reject(errf(StateError, "commit only allowed while frozen"))
				return
			}
			rec = grp.commit()
			ctx.ok(fmt.Sprintf("snapshot=%d committed, point=%d", rec.ID, rec.Point))
		})
	if e0 != nil {
		return nil, e0
	}
	if e != nil {
		return nil, e
	}
	return rec, nil
}

func (c *Coordinator) Abort(t int64, groupID string) error {
	var e *Error
	e0 := c.run("abort", fmt.Sprintf("group=%q", groupID), t,
		func() *Error {
			if !nonEmpty(groupID) {
				return errf(InvalidArgument, "group id required")
			}
			return nil
		},
		groupID,
		func(ctx *opCtx) {
			grp, ok := c.groups[groupID]
			if !ok {
				e = ctx.reject(errf(NotFound, "group %q not found", groupID))
				return
			}
			ctx.timeout(grp)
			if grp.snap == nil {
				e = ctx.reject(errf(StateError, "no snapshot in progress"))
				return
			}
			grp.abort()
			ctx.ok("aborted")
		})
	if e0 != nil {
		return e0
	}
	if e != nil {
		return e
	}
	return nil
}

func (c *Coordinator) Write(t int64, volumeID, data string) (WriteResult, error) {
	var res WriteResult
	var e *Error
	e0 := c.run("write", fmt.Sprintf("volume=%q data=%q", volumeID, data), t,
		func() *Error {
			if !nonEmpty(volumeID) || data == "" {
				return errf(InvalidArgument, "volume id and non-empty data required")
			}
			return nil
		},
		"",
		func(ctx *opCtx) {
			vol, ok := c.volumes[volumeID]
			if !ok {
				e = ctx.reject(errf(NotFound, "volume %q not found", volumeID))
				return
			}
			grp := vol.group
			if grp != nil {
				ctx.timeout(grp)
			}
			// O(1) queueing decision: nil check, one phase read and, in
			// freezing, one hash lookup. It never scans members or queues.
			if grp != nil && grp.needsQueue(vol.id) {
				if vol.queueFull() {
					e = ctx.reject(errf(QueueFull, "freeze queue full for volume %q", volumeID))
					return
				}
				vol.enqueue(t, data)
				res.Queued = true
				ctx.ok(fmt.Sprintf("queued=%d/%d", len(vol.queue), vol.capacity))
				return
			}
			res.Seq = vol.apply(data)
			ctx.ok(fmt.Sprintf("applied seq=%d", res.Seq))
		})
	if e0 != nil {
		return WriteResult{}, e0
	}
	if e != nil {
		return WriteResult{}, e
	}
	return res, nil
}

func (c *Coordinator) GetVolume(t int64, volumeID string) (VolumeStatus, error) {
	var st VolumeStatus
	var e *Error
	e0 := c.run("get_volume", fmt.Sprintf("volume=%q", volumeID), t,
		func() *Error {
			if !nonEmpty(volumeID) {
				return errf(InvalidArgument, "volume id required")
			}
			return nil
		},
		"",
		func(ctx *opCtx) {
			vol, ok := c.volumes[volumeID]
			if !ok {
				e = ctx.reject(errf(NotFound, "volume %q not found", volumeID))
				return
			}
			if vol.group != nil {
				ctx.timeout(vol.group)
			}
			st = VolumeStatus{
				ID:            vol.id,
				Seq:           vol.seq,
				QueuedWrites:  len(vol.queue),
				QueueCapacity: vol.capacity,
			}
			if vol.group != nil {
				st.GroupID = vol.group.id
			}
			ctx.ok(fmt.Sprintf("seq=%d queued=%d", st.Seq, st.QueuedWrites))
		})
	if e0 != nil {
		return VolumeStatus{}, e0
	}
	if e != nil {
		return VolumeStatus{}, e
	}
	return st, nil
}

func (c *Coordinator) GetGroup(t int64, groupID string) (GroupStatus, error) {
	var st GroupStatus
	var e *Error
	e0 := c.run("get_group", fmt.Sprintf("group=%q", groupID), t,
		func() *Error {
			if !nonEmpty(groupID) {
				return errf(InvalidArgument, "group id required")
			}
			return nil
		},
		groupID,
		func(ctx *opCtx) {
			grp, ok := c.groups[groupID]
			if !ok {
				e = ctx.reject(errf(NotFound, "group %q not found", groupID))
				return
			}
			ctx.timeout(grp)
			st.ID = grp.id
			st.Members = make([]string, 0, len(grp.members))
			for _, v := range grp.members {
				st.Members = append(st.Members, v.id)
			}
			if grp.snap != nil {
				st.Phase = grp.snap.phase
				st.Deadline = grp.snap.deadline
				st.Point = grp.snap.point
				st.SnapshotID = grp.snap.id
				st.Confirmed = sortedKeys(grp.snap.confirmed)
			}
			ctx.ok(fmt.Sprintf("phase=%s members=%d", st.Phase, len(st.Members)))
		})
	if e0 != nil {
		return GroupStatus{}, e0
	}
	if e != nil {
		return GroupStatus{}, e
	}
	return st, nil
}

// LastSnapshot returns the most recently committed snapshot record of a
// group, or nil if none was committed. Aborted snapshots leave no record.
func (c *Coordinator) LastSnapshot(t int64, groupID string) (*SnapshotRecord, error) {
	var rec *SnapshotRecord
	var e *Error
	e0 := c.run("last_snapshot", fmt.Sprintf("group=%q", groupID), t,
		func() *Error {
			if !nonEmpty(groupID) {
				return errf(InvalidArgument, "group id required")
			}
			return nil
		},
		groupID,
		func(ctx *opCtx) {
			grp, ok := c.groups[groupID]
			if !ok {
				e = ctx.reject(errf(NotFound, "group %q not found", groupID))
				return
			}
			ctx.timeout(grp)
			rec = grp.lastSnap
			if rec == nil {
				ctx.ok("no snapshot record")
			} else {
				ctx.ok(fmt.Sprintf("snapshot=%d point=%d", rec.ID, rec.Point))
			}
		})
	if e0 != nil {
		return nil, e0
	}
	return rec, e
}
