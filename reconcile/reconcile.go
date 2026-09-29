package reconcile

import (
	"log/slog"

	"ontology/reconcile/internal/logger"
)

// Comparison records one compared interval and whether its hashes matched.
type Comparison struct {
	Level    int
	Lo       uint64
	Hi       uint64
	HashesEq bool
}

// Report is the deterministic reconciliation result.
type Report struct {
	DiffKeys    []uint64
	Comparisons []Comparison
}

// frame is one pending interval in the top-down drill-down.
type frame struct {
	level int
	index uint64
}

// Reconcile locates differing keys by drilling only into intervals whose
// combined hashes disagree. The two snapshots must describe the same key
// space and fanout; otherwise ErrShapeMismatch is returned. Repeated calls on
// the same pair always yield identical diff keys and comparison order:
// intervals are visited depth-first, children left to right.
func Reconcile(a, b *Snapshot) (*Report, error) {
	if a == nil || b == nil {
		return nil, ErrShapeMismatch
	}
	if a.fanout != b.fanout || a.keyMax != b.keyMax || a.tree.depth != b.tree.depth {
		return nil, ErrShapeMismatch
	}

	report := &Report{DiffKeys: []uint64{}, Comparisons: []Comparison{}}
	log := logger.Get()
	log.Info("reconcile start",
		slog.Uint64("fanout", a.fanout),
		slog.Uint64("key_max", a.keyMax),
		slog.Int("keys_a", a.Len()),
		slog.Int("keys_b", b.Len()),
	)

	stack := []frame{{level: 0, index: 0}}
	for len(stack) > 0 {
		last := len(stack) - 1
		cur := stack[last]
		stack = stack[:last]

		ha := a.tree.levels[cur.level][cur.index]
		hb := b.tree.levels[cur.level][cur.index]
		eq := ha == hb
		lo := a.tree.los[cur.level][cur.index]
		hi := a.tree.his[cur.level][cur.index]
		report.Comparisons = append(report.Comparisons, Comparison{
			Level: cur.level, Lo: lo, Hi: hi, HashesEq: eq,
		})

		if eq {
			log.Debug("interval match, skip subtree",
				slog.Int("level", cur.level),
				slog.Uint64("lo", lo), slog.Uint64("hi", hi),
				slog.String("basis", "combined hashes equal"),
			)
			continue
		}

		leafLevel := a.tree.depth - 1
		if cur.level == leafLevel {
			key := lo
			va, inA := a.values[key]
			vb, inB := b.values[key]
			report.DiffKeys = append(report.DiffKeys, key)
			log.Info("diff key at leaf",
				slog.Uint64("key", key),
				slog.Bool("present_a", inA), slog.Uint64("value_a", va),
				slog.Bool("present_b", inB), slog.Uint64("value_b", vb),
				slog.String("basis", "leaf hashes differ"),
			)
			continue
		}

		childCount := uint64(len(a.tree.levels[cur.level+1]))
		childFirst := cur.index * a.fanout
		childLast := childFirst + a.fanout
		if childLast > childCount {
			childLast = childCount
		}
		log.Debug("interval mismatch, drilling down",
			slog.Int("level", cur.level),
			slog.Uint64("lo", lo), slog.Uint64("hi", hi),
			slog.Uint64("children", childLast-childFirst),
			slog.String("basis", "combined hashes differ"),
		)
		for child := childLast; child > childFirst; child-- {
			childIndex := child - 1
			childLo := a.tree.los[cur.level+1][childIndex]
			childHi := a.tree.his[cur.level+1][childIndex]
			if childLo == childHi {
				continue // padded position beyond keyMax, never part of the key space
			}
			stack = append(stack, frame{level: cur.level + 1, index: childIndex})
		}
	}

	log.Info("reconcile done",
		slog.Any("diff_keys", report.DiffKeys),
		slog.Int("comparisons", len(report.Comparisons)),
	)
	return report, nil
}
