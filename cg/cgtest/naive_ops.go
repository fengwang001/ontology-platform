package cgtest

func (n *Naive) CreateGroup(t int64, gid string, vols []string) *NaiveError {
	if gid == "" || len(vols) < 2 || len(vols) > 16 {
		return &NaiveError{InvalidArgument}
	}
	for i, v := range vols {
		if v == "" {
			return &NaiveError{InvalidArgument}
		}
		for j := 0; j < i; j++ {
			if vols[j] == v {
				return &NaiveError{InvalidArgument}
			}
		}
	}
	if e := n.clock(t); e != nil {
		return e
	}
	if n.findGroup(gid) != nil {
		return &NaiveError{Conflict}
	}
	for _, vid := range vols {
		if n.findVolume(vid) != nil {
			return &NaiveError{Conflict}
		}
	}
	g := &nGroup{id: gid, members: append([]string(nil), vols...)}
	n.groups = append(n.groups, g)
	for _, vid := range vols {
		n.volumes = append(n.volumes, &nVolume{id: vid, cap: n.defaultCap})
	}
	return nil
}

func (n *Naive) AddMembers(t int64, gid string, vols []string) *NaiveError {
	if gid == "" || len(vols) == 0 {
		return &NaiveError{InvalidArgument}
	}
	for i, v := range vols {
		if v == "" {
			return &NaiveError{InvalidArgument}
		}
		for j := 0; j < i; j++ {
			if vols[j] == v {
				return &NaiveError{InvalidArgument}
			}
		}
	}
	if e := n.clock(t); e != nil {
		return e
	}
	g := n.findGroup(gid)
	if g == nil {
		return &NaiveError{NotFound}
	}
	n.timeout(g)
	if g.snap != nil {
		return &NaiveError{StateError}
	}
	if len(g.members)+len(vols) > 16 {
		return &NaiveError{InvalidArgument}
	}
	for _, vid := range vols {
		if groupContains(g, vid) {
			return &NaiveError{InvalidArgument}
		}
		if n.findVolume(vid) != nil {
			return &NaiveError{Conflict}
		}
	}
	for _, vid := range vols {
		g.members = append(g.members, vid)
		n.volumes = append(n.volumes, &nVolume{id: vid, cap: n.defaultCap})
	}
	return nil
}

func (n *Naive) RemoveMembers(t int64, gid string, vols []string) *NaiveError {
	if gid == "" || len(vols) == 0 {
		return &NaiveError{InvalidArgument}
	}
	for i, v := range vols {
		if v == "" {
			return &NaiveError{InvalidArgument}
		}
		for j := 0; j < i; j++ {
			if vols[j] == v {
				return &NaiveError{InvalidArgument}
			}
		}
	}
	if e := n.clock(t); e != nil {
		return e
	}
	g := n.findGroup(gid)
	if g == nil {
		return &NaiveError{NotFound}
	}
	n.timeout(g)
	if g.snap != nil {
		return &NaiveError{StateError}
	}
	for _, vid := range vols {
		if !groupContains(g, vid) {
			return &NaiveError{NotFound}
		}
	}
	if len(g.members)-len(vols) < 2 {
		return &NaiveError{InvalidArgument}
	}
	remove := map[string]bool{}
	for _, v := range vols {
		remove[v] = true
	}
	kept := g.members[:0]
	for _, m := range g.members {
		if remove[m] {
			for i, vv := range n.volumes {
				if vv.id == m {
					n.volumes = append(n.volumes[:i], n.volumes[i+1:]...)
					break
				}
			}
			continue
		}
		kept = append(kept, m)
	}
	g.members = kept
	return nil
}

func (n *Naive) BeginSnapshot(t int64, gid string, deadline int64) (uint64, *NaiveError) {
	if gid == "" || deadline < t {
		return 0, &NaiveError{InvalidArgument}
	}
	if e := n.clock(t); e != nil {
		return 0, e
	}
	g := n.findGroup(gid)
	if g == nil {
		return 0, &NaiveError{NotFound}
	}
	n.timeout(g)
	if g.snap != nil {
		return 0, &NaiveError{StateError}
	}
	id := n.nextSnap
	n.nextSnap++
	g.snap = &nSnapshot{id: id, phase: 0, deadline: deadline}
	return id, nil
}

func (n *Naive) ConfirmFreeze(t int64, gid, vid string) (bool, *NaiveError) {
	if gid == "" || vid == "" {
		return false, &NaiveError{InvalidArgument}
	}
	if e := n.clock(t); e != nil {
		return false, e
	}
	g := n.findGroup(gid)
	if g == nil {
		return false, &NaiveError{NotFound}
	}
	n.timeout(g)
	if g.snap == nil || g.snap.phase != 0 {
		return false, &NaiveError{StateError}
	}
	if !groupContains(g, vid) {
		return false, &NaiveError{NotFound}
	}
	if confirmedContains(g.snap, vid) {
		return false, &NaiveError{DuplicateConfirm}
	}
	s := g.snap
	s.confirmed = append(s.confirmed, vid)
	if len(s.confirmed) < len(g.members) {
		return false, nil
	}
	s.phase = 1
	s.point = t
	s.cutoffs = map[string]uint64{}
	for _, m := range g.members {
		s.cutoffs[m] = n.findVolume(m).seq
	}
	return true, nil
}

func (n *Naive) Commit(t int64, gid string) (uint64, map[string]uint64, int64, *NaiveError) {
	if gid == "" {
		return 0, nil, 0, &NaiveError{InvalidArgument}
	}
	if e := n.clock(t); e != nil {
		return 0, nil, 0, e
	}
	g := n.findGroup(gid)
	if g == nil {
		return 0, nil, 0, &NaiveError{NotFound}
	}
	n.timeout(g)
	if g.snap == nil || g.snap.phase != 1 {
		return 0, nil, 0, &NaiveError{StateError}
	}
	s := g.snap
	cut := map[string]uint64{}
	for k, val := range s.cutoffs {
		cut[k] = val
	}
	id, pt := s.id, s.point
	g.lastID, g.lastPt, g.lastCut = id, pt, cut
	n.unfreeze(g)
	return id, cut, pt, nil
}

func (n *Naive) Abort(t int64, gid string) *NaiveError {
	if gid == "" {
		return &NaiveError{InvalidArgument}
	}
	if e := n.clock(t); e != nil {
		return e
	}
	g := n.findGroup(gid)
	if g == nil {
		return &NaiveError{NotFound}
	}
	n.timeout(g)
	if g.snap == nil {
		return &NaiveError{StateError}
	}
	n.unfreeze(g)
	return nil
}

// NWriteResult reports the modeled outcome.
type NWriteResult struct {
	Seq    uint64
	Queued bool
}

func (n *Naive) Write(t int64, vid, data string) (NWriteResult, *NaiveError) {
	if vid == "" || data == "" {
		return NWriteResult{}, &NaiveError{InvalidArgument}
	}
	if e := n.clock(t); e != nil {
		return NWriteResult{}, e
	}
	v := n.findVolume(vid)
	if v == nil {
		return NWriteResult{}, &NaiveError{NotFound}
	}
	var g *nGroup
	for _, gg := range n.groups {
		if groupContains(gg, vid) {
			g = gg
			break
		}
	}
	n.timeout(g)
	queueNow := false
	if g != nil && g.snap != nil {
		if g.snap.phase == 1 {
			queueNow = true
		} else if confirmedContains(g.snap, vid) {
			queueNow = true
		}
	}
	if queueNow {
		if len(v.queue) >= v.cap {
			return NWriteResult{}, &NaiveError{QueueFull}
		}
		v.queue = append(v.queue, data)
		return NWriteResult{Queued: true}, nil
	}
	v.seq++
	return NWriteResult{Seq: v.seq}, nil
}

// Seqs returns per-volume applied sequence numbers.
func (n *Naive) Seqs() map[string]uint64 {
	out := map[string]uint64{}
	for _, v := range n.volumes {
		out[v.id] = v.seq
	}
	return out
}

// SnapshotSeqs is the same data as Seqs but is named for state comparison
// (which must not itself advance the model clock).
func (n *Naive) SnapshotSeqs() map[string]uint64 { return n.Seqs() }

// SnapshotQueued is the same data as Queued without clock advancement.
func (n *Naive) SnapshotQueued() map[string]int { return n.Queued() }

// groupOfVolume finds the group currently containing vid, independent of
// any possibly stale per-volume index, via linear scans.
func (n *Naive) groupOfVolume(vid string) *nGroup {
	for _, g := range n.groups {
		if groupContains(g, vid) {
			return g
		}
	}
	return nil
}

// ReadVolume mirrors a read-only GetVolume: it advances time and performs
// timeout detection on the volume's group.
func (n *Naive) ReadVolume(t int64, vid string) *NaiveError {
	if vid == "" {
		return &NaiveError{InvalidArgument}
	}
	if e := n.clock(t); e != nil {
		return e
	}
	if n.findVolume(vid) == nil {
		return &NaiveError{NotFound}
	}
	n.timeout(n.groupOfVolume(vid))
	return nil
}

// Queued returns per-volume queued write counts.
func (n *Naive) Queued() map[string]int {
	out := map[string]int{}
	for _, v := range n.volumes {
		out[v.id] = len(v.queue)
	}
	return out
}

// Phase reports 0=idle,1=freezing,2=frozen per group (missing => -1).
func (n *Naive) phase(gid string) int {
	g := n.findGroup(gid)
	if g == nil {
		return -1
	}
	if g.snap == nil {
		return 0
	}
	return g.snap.phase + 1
}

// Phase is the pure, clock-free accessor used by state comparison.
func (n *Naive) Phase(gid string) int { return n.phase(gid) }

// ReadGroup mirrors a read-only GetGroup: advances time and checks
// timeouts before reporting existence.
func (n *Naive) ReadGroup(t int64, gid string) *NaiveError {
	if gid == "" {
		return &NaiveError{InvalidArgument}
	}
	if e := n.clock(t); e != nil {
		return e
	}
	g := n.findGroup(gid)
	if g == nil {
		return &NaiveError{NotFound}
	}
	n.timeout(g)
	return nil
}

// LastSnapshot returns the last committed record's id, point and cutoffs.
// As a read operation it still carries time and performs timeout checks.
func (n *Naive) LastSnapshot(t int64, gid string) (uint64, int64, map[string]uint64, bool, *NaiveError) {
	if gid == "" {
		return 0, 0, nil, false, &NaiveError{InvalidArgument}
	}
	if e := n.clock(t); e != nil {
		return 0, 0, nil, false, e
	}
	g := n.findGroup(gid)
	if g == nil {
		return 0, 0, nil, false, &NaiveError{NotFound}
	}
	n.timeout(g)
	if g.lastCut == nil {
		return 0, 0, nil, false, nil
	}
	cut := map[string]uint64{}
	for k, val := range g.lastCut {
		cut[k] = val
	}
	return g.lastID, g.lastPt, cut, true, nil
}

// Clock reports the last accepted operation time.
func (n *Naive) Clock() int64 { return n.lastTime }
