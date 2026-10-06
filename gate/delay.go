package gate

import (
	"fmt"
	"sort"
)

// DelayArrival 推迟航班到达时刻（只允许推迟）。
func (s *System) DelayArrival(flightID string, newArr, now int) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delayLocked(flightID, true, newArr, now)
}

// DelayDeparture 推迟航班起飞时刻（只允许推迟）。
func (s *System) DelayDeparture(flightID string, newDep, now int) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.delayLocked(flightID, false, newDep, now)
}

// delayLocked 先全量预计算新区间、分段结构与所有冲突的裁决结果，
// 任一对裁决失败（双方均不可挤占）则整体拒绝、不留任何改动。
func (s *System) delayLocked(flightID string, isArr bool, t, now int) Result {
	if now < 0 || t < 0 {
		return reject(ReasonInvalidParam, "negative time: t=%d now=%d", t, now)
	}
	if now < s.now {
		return reject(ReasonClockSkew, "now=%d < clock=%d", now, s.now)
	}
	s.pruneAll(now)
	fs, ok := s.flights[flightID]
	if !ok {
		return reject(ReasonNotFound, "flight %s not found", flightID)
	}
	cur := fs.f.CurDep
	if isArr {
		cur = fs.f.CurArr
	}
	if t < cur {
		return reject(ReasonInvalidParam, "only postponement allowed: %d < current %d", t, cur)
	}
	newArr, newDep := fs.f.CurArr, fs.f.CurDep
	if isArr {
		newArr = t
	} else {
		newDep = t
	}
	if newArr > newDep {
		return reject(ReasonInvalidParam, "arrival %d after departure %d", newArr, newDep)
	}
	if isArr && now >= fs.f.CurArr {
		return reject(ReasonImmutable, "flight %s arrived at %d, arrival immutable at now=%d",
			flightID, fs.f.CurArr, now)
	}

	newSegs := buildSegs(fs, newArr, newDep, s.cfg, fs.segs)

	type conflict struct {
		my    SegKind
		other *segment
	}
	var conflicts []conflict
	for _, k := range []SegKind{SegWhole, SegDeplane, SegBoard} {
		ns, ok := newSegs[k]
		if !ok || ns.gate == "" {
			continue
		}
		g := s.gates[ns.gate]
		for _, sg := range g.tree.overlap(ns.start, ns.end, &s.visits) {
			if sg.owner != fs {
				conflicts = append(conflicts, conflict{my: k, other: sg})
			}
		}
		if fs.f.Level == MaxLevel {
			for _, adjID := range g.adj {
				for _, sg := range s.gates[adjID].tree.overlap(ns.start, ns.end, &s.visits) {
					if sg.owner != fs && sg.owner.f.Level == MaxLevel {
						conflicts = append(conflicts, conflict{my: k, other: sg})
					}
				}
			}
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		a, b := conflicts[i].other, conflicts[j].other
		if a.start != b.start {
			return a.start < b.start
		}
		if a.owner.f.ID != b.owner.f.ID {
			return a.owner.f.ID < b.owner.f.ID
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return conflicts[i].my < conflicts[j].my
	})

	meProtected := protectedNow(newArr, newDep, fs.f.BoardLead, now)
	myLost := map[SegKind]bool{}
	bumpedSegs := map[*segment]bool{}
	bumpedSet := map[string]bool{}
	for _, c := range conflicts {
		if myLost[c.my] || bumpedSegs[c.other] {
			continue
		}
		o := c.other.owner
		otherProtected := protectedNow(o.f.CurArr, o.f.CurDep, o.f.BoardLead, now)
		switch {
		case meProtected && otherProtected:
			return reject(ReasonNotBumpable,
				"both %s and %s arrived/boarding, update rejected", flightID, o.f.ID)
		case otherProtected:
			myLost[c.my] = true
		case meProtected:
			bumpedSegs[c.other] = true
			bumpedSet[o.f.ID] = true
		default:
			if higherPriority(&fs.f, &o.f) {
				bumpedSegs[c.other] = true
				bumpedSet[o.f.ID] = true
			} else {
				myLost[c.my] = true
			}
		}
	}

	for _, sg := range fs.segs {
		if sg.gate != "" {
			s.gates[sg.gate].tree.remove(sg.key())
		}
	}
	for sg := range bumpedSegs {
		if sg.gate != "" {
			s.gates[sg.gate].tree.remove(sg.key())
			sg.gate = ""
		}
	}
	lostSelf := false
	for k, ns := range newSegs {
		if myLost[k] {
			ns.gate = ""
			lostSelf = true
		}
		if ns.gate != "" {
			s.gates[ns.gate].tree.insert(ns.key(), ns.end, ns)
		}
	}
	fs.f.CurArr, fs.f.CurDep = newArr, newDep
	fs.segs = newSegs

	var bumped []string
	for id := range bumpedSet {
		bumped = append(bumped, id)
	}
	if lostSelf {
		bumped = append(bumped, flightID)
	}
	sort.Strings(bumped)
	s.accept(now)
	return Result{OK: true, Reason: ReasonOK, Bumped: bumped, Detail: fmt.Sprintf(
		"flight %s delayed to [%d,%d], bumped=%v", flightID, newArr, newDep, bumped)}
}
