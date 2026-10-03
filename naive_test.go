package ontology

import (
	"fmt"
	"sort"
	"strings"
)

// naiveEntry is one live version in the straightforward reference model.
type naiveEntry struct {
	t      int64
	size   int64
	pinned bool
}

type naiveTrash struct {
	t, size, deletedAt int64
}

// naiveCleaner is a literal transcription of the specification into the
// simplest possible Go data structures (sorted slices). It shares no code
// with Cleaner and is used for differential testing.
type naiveCleaner struct {
	rules        []Rule
	maxCount     int64
	maxBytes     int64
	trashTTL     int64
	lastNow      int64
	live         map[string][]*naiveEntry
	trash        map[string][]*naiveTrash
	thinnedExams int
}

func newNaive(rules []Rule, maxCount, maxBytes, trashTTL int64) *naiveCleaner {
	return &naiveCleaner{
		rules:    rules,
		maxCount: maxCount,
		maxBytes: maxBytes,
		trashTTL: trashTTL,
		live:     make(map[string][]*naiveEntry),
		trash:    make(map[string][]*naiveTrash),
	}
}

func (n *naiveCleaner) find(file string, t int64) *naiveEntry {
	for _, e := range n.live[file] {
		if e.t == t {
			return e
		}
	}
	return nil
}

func (n *naiveCleaner) findTrash(file string, t int64) *naiveTrash {
	for _, tr := range n.trash[file] {
		if tr.t == t {
			return tr
		}
	}
	return nil
}

func (n *naiveCleaner) add(now int64, file string, t, size int64) error {
	if now < n.lastNow {
		return ErrClock
	}
	if file == "" || t < 0 || t > now || size < 1 {
		return ErrInvalid
	}
	if n.findTrash(file, t) != nil || n.find(file, t) != nil {
		return ErrDuplicate
	}
	var sum int64
	for _, e := range n.live[file] {
		sum += e.size
	}
	if size > maxTotalBytes-sum {
		return ErrTooLarge
	}
	n.lastNow = now
	n.live[file] = append(n.live[file], &naiveEntry{t: t, size: size})
	sort.Slice(n.live[file], func(i, j int) bool { return n.live[file][i].t < n.live[file][j].t })
	return nil
}

func (n *naiveCleaner) pin(file string, t int64, pinned bool) error {
	if file == "" {
		return ErrInvalid
	}
	if e := n.find(file, t); e != nil {
		e.pinned = pinned
		return nil
	}
	return ErrNoVersion
}

func (n *naiveCleaner) clean(now int64) (purged []PurgedVersion, deleted []Deletion) {
	if now < n.lastNow {
		return nil, nil
	}
	n.lastNow = now

	// Trash purge.
	files := make([]string, 0)
	seen := map[string]bool{}
	for f := range n.trash {
		seen[f] = true
	}
	for f := range n.live {
		seen[f] = true
	}
	for f := range seen {
		files = append(files, f)
	}
	sort.Strings(files)

	for _, f := range files {
		kept := n.trash[f][:0]
		var purgedTs []int64
		for _, tr := range n.trash[f] {
			if now-tr.deletedAt >= n.trashTTL {
				purgedTs = append(purgedTs, tr.t)
			} else {
				kept = append(kept, tr)
			}
		}
		n.trash[f] = kept
		sort.Slice(purgedTs, func(i, j int) bool { return purgedTs[i] < purgedTs[j] })
		for _, t := range purgedTs {
			purged = append(purged, PurgedVersion{File: f, T: t})
		}
	}

	n.thinnedExams = 0

	for _, f := range files {
		all := n.live[f]
		if len(all) == 0 {
			continue
		}
		latest := all[len(all)-1].t

		// Aged.
		survivors := []*naiveEntry{}
		for _, e := range all {
			age := now - e.t
			inTier := false
			for _, r := range n.rules {
				if age < r.Until {
					inTier = true
					break
				}
			}
			if !inTier && !e.pinned && e.t != latest {
				deleted = append(deleted, Deletion{f, e.t, ReasonAged})
				n.trash[f] = append(n.trash[f], &naiveTrash{e.t, e.size, now})
				continue
			}
			survivors = append(survivors, e)
		}

		// Thinned: every remaining version examined exactly once.
		n.thinnedExams += len(survivors)
		kept := []*naiveEntry{}
		var prev *naiveEntry
		for _, e := range survivors {
			if e.pinned || e.t == latest || prev == nil {
				kept = append(kept, e)
				prev = e
				continue
			}
			age := now - e.t
			var step int64
			for _, r := range n.rules {
				if age < r.Until {
					step = r.Step
					break
				}
			}
			if e.t-prev.t < step {
				deleted = append(deleted, Deletion{f, e.t, ReasonThinned})
				n.trash[f] = append(n.trash[f], &naiveTrash{e.t, e.size, now})
				continue
			}
			kept = append(kept, e)
			prev = e
		}

		// Limits: repeatedly drop the oldest unpinned, non-latest version.
		var totalBytes int64
		for _, e := range kept {
			totalBytes += e.size
		}
		totalCount := int64(len(kept))
		for totalCount > n.maxCount || totalBytes > n.maxBytes {
			var victim *naiveEntry
			for _, e := range kept {
				if !e.pinned && e.t != latest {
					victim = e
					break
				}
			}
			if victim == nil {
				break
			}
			reason := ReasonOverBytes
			if totalCount > n.maxCount {
				reason = ReasonOverCount
			}
			deleted = append(deleted, Deletion{f, victim.t, reason})
			n.trash[f] = append(n.trash[f], &naiveTrash{victim.t, victim.size, now})
			totalCount--
			totalBytes -= victim.size
			next := kept[:0]
			for _, e := range kept {
				if e != victim {
					next = append(next, e)
				}
			}
			kept = next
		}

		n.live[f] = kept
	}

	sort.SliceStable(deleted, func(i, j int) bool {
		if deleted[i].File != deleted[j].File {
			return deleted[i].File < deleted[j].File
		}
		return deleted[i].T < deleted[j].T
	})
	sort.SliceStable(purged, func(i, j int) bool {
		if purged[i].File != purged[j].File {
			return purged[i].File < purged[j].File
		}
		return purged[i].T < purged[j].T
	})
	return purged, deleted
}

func (n *naiveCleaner) undelete(now int64, file string, t int64) error {
	if now < n.lastNow {
		return ErrClock
	}
	if file == "" {
		return ErrInvalid
	}
	tr := n.findTrash(file, t)
	if tr == nil || now-tr.deletedAt >= n.trashTTL {
		return ErrNoVersion
	}
	n.lastNow = now
	next := n.trash[file][:0]
	for _, x := range n.trash[file] {
		if x != tr {
			next = append(next, x)
		}
	}
	n.trash[file] = next
	n.live[file] = append(n.live[file], &naiveEntry{t: tr.t, size: tr.size})
	sort.Slice(n.live[file], func(i, j int) bool { return n.live[file][i].t < n.live[file][j].t })
	return nil
}

func (n *naiveCleaner) versions(file string) []Version {
	out := []Version{}
	for _, e := range n.live[file] {
		out = append(out, Version{e.t, e.size, e.pinned})
	}
	return out
}

func (n *naiveCleaner) totals(file string) (int64, int64) {
	var count, bytes int64
	for _, e := range n.live[file] {
		count++
		bytes += e.size
	}
	return count, bytes
}

func (n *naiveCleaner) trashList(file string) []TrashItem {
	out := []TrashItem{}
	for _, tr := range n.trash[file] {
		out = append(out, TrashItem{tr.t, tr.size, tr.deletedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}

// stateSig captures everything observable about both implementations.
type stateSig struct {
	live  string
	trash string
}

func sigCleaner(c *Cleaner, files []string) stateSig {
	var b strings.Builder
	for _, f := range files {
		for _, v := range c.Versions(f) {
			p := byte('-')
			if v.Pinned {
				p = byte('P')
			}
			fmt.Fprintf(&b, "%s@%d/%d%c;", f, v.T, v.Size, p)
		}
		for _, tr := range c.Trash(f) {
			fmt.Fprintf(&b, "%s@%d#%d@%d;", f, tr.T, tr.Size, tr.DeletedAt)
		}
	}
	return stateSig{b.String(), ""}
}

func sigNaive(n *naiveCleaner, files []string) stateSig {
	var b strings.Builder
	for _, f := range files {
		for _, v := range n.versions(f) {
			p := byte('-')
			if v.Pinned {
				p = byte('P')
			}
			fmt.Fprintf(&b, "%s@%d/%d%c;", f, v.T, v.Size, p)
		}
		for _, tr := range n.trashList(f) {
			fmt.Fprintf(&b, "%s@%d#%d@%d;", f, tr.T, tr.Size, tr.DeletedAt)
		}
	}
	return stateSig{b.String(), ""}
}
