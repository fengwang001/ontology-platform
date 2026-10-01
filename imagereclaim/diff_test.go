package imagereclaim

import (
	"fmt"
	"sort"
	"strings"
)

type opResult struct {
	errCode ErrCode
	layerID string
	gc      GCResult
}

func opResultFrom(e error, res GCResult) opResult {
	out := opResult{gc: res}
	if e != nil {
		er := e.(*Error)
		out.errCode, out.layerID = er.Code, er.LayerID
	}
	return out
}

func describeOp(op randomOp) string {
	switch op.kind {
	case opBegin, opPull:
		var ls []string
		for _, l := range op.layers {
			ls = append(ls, fmt.Sprintf("%s=%d", l.ID, l.Size))
		}
		name := "BeginPull"
		if op.kind == opPull {
			name = "Pull"
		}
		return fmt.Sprintf("%s(%s,[%s],%d)", name, op.img, strings.Join(ls, ","), op.now)
	case opCommit:
		return fmt.Sprintf("CommitPull(%s,%d)", op.img, op.now)
	case opAbort:
		return fmt.Sprintf("AbortPull(%s,%d)", op.img, op.now)
	case opRun:
		return fmt.Sprintf("Run(%s,%d)", op.img, op.now)
	case opStop:
		return fmt.Sprintf("Stop(%s,%d)", op.img, op.now)
	default:
		return fmt.Sprintf("GC(%d,h=%d,l=%d,age=%d)", op.now, op.high, op.low, op.minAge)
	}
}

func formatOutcome(o opResult) string {
	if o.errCode != 0 {
		return fmt.Sprintf("ERR(%d:%s)", o.errCode, o.layerID)
	}
	if len(o.gc.Deleted) == 0 && !o.gc.Short && o.gc.Freed == 0 {
		return "OK"
	}
	return fmt.Sprintf("OK(deleted=%v freed=%d short=%v)", o.gc.Deleted, o.gc.Freed, o.gc.Short)
}

func formatNaive(o naiveOutcome) string {
	if o.errCode != 0 {
		return fmt.Sprintf("ERR(%d:%s)", o.errCode, o.layerID)
	}
	if len(o.gc.Deleted) == 0 && !o.gc.Short && o.gc.Freed == 0 {
		return "OK"
	}
	return fmt.Sprintf("OK(deleted=%v freed=%d short=%v)", o.gc.Deleted, o.gc.Freed, o.gc.Short)
}

func outcomeMatch(o opResult, n naiveOutcome) bool {
	if o.errCode != n.errCode || o.layerID != n.layerID {
		return false
	}
	return equalStrings(o.gc.Deleted, n.gc.Deleted) && o.gc.Freed == n.gc.Freed && o.gc.Short == n.gc.Short
}

func fullSnapshot(r *Reclaimer) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.images))
	for id := range r.images {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		im := r.images[id]
		var ls []string
		for _, l := range im.layers {
			ls = append(ls, l.ID)
		}
		state := "ready"
		if im.state == statePulling {
			state = "pulling"
		}
		fmt.Fprintf(&b, "%s{%s last=%d run=%d layers=%s};", id, state, im.lastUsed, im.run, strings.Join(ls, ","))
	}
	layerIDs := make([]string, 0, len(r.layers))
	for id, info := range r.layers {
		layerIDs = append(layerIDs, fmt.Sprintf("%s=%dx%d", id, info.size, info.refs))
	}
	sort.Strings(layerIDs)
	fmt.Fprintf(&b, "|layers:%s|used:%d", strings.Join(layerIDs, ","), r.used)
	return b.String()
}

func naiveSnapshot(n *naiveReclaimer) string {
	ids := make([]string, 0, len(n.imgs))
	for id := range n.imgs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		im := n.imgs[id]
		var ls []string
		for _, l := range im.layers {
			ls = append(ls, l.ID)
		}
		state := "ready"
		if !im.ready {
			state = "pulling"
		}
		fmt.Fprintf(&b, "%s{%s last=%d run=%d layers=%s};", id, state, im.lastUsed, im.run, strings.Join(ls, ","))
	}
	refs := make(map[string]int)
	sizes := make(map[string]int64)
	for _, im := range n.imgs {
		seen := map[string]bool{}
		for _, l := range im.layers {
			if !seen[l.ID] {
				refs[l.ID]++
				sizes[l.ID] = l.Size
				seen[l.ID] = true
			}
		}
	}
	layerIDs := make([]string, 0, len(refs))
	for id := range refs {
		layerIDs = append(layerIDs, fmt.Sprintf("%s=%dx%d", id, sizes[id], refs[id]))
	}
	sort.Strings(layerIDs)
	fmt.Fprintf(&b, "|layers:%s|used:%d", strings.Join(layerIDs, ","), n.usedNow())
	return b.String()
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
