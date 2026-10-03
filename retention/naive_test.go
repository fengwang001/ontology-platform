package retention

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveCleaner is a deliberately straightforward reference implementation of
// the specification, written step by step from the rules. The randomized
// test below cross-checks the real Cleaner against it.

type naiveVersion struct {
	t         int64
	size      int64
	pinned    bool
	inTrash   bool
	deletedAt int64
}

type naiveCleaner struct {
	rules    []Rule
	maxCount int64
	maxBytes int64
	trashTTL int64
	hasNow   bool
	lastNow  int64
	files    map[string][]*naiveVersion
}

func newNaiveCleaner(rules []Rule, maxCount, maxBytes, trashTTL int64) *naiveCleaner {
	return &naiveCleaner{
		rules:    rules,
		maxCount: maxCount,
		maxBytes: maxBytes,
		trashTTL: trashTTL,
		files:    make(map[string][]*naiveVersion),
	}
}

func (n *naiveCleaner) clockErr(now int64) error {
	if n.hasNow && now < n.lastNow {
		return ErrClock
	}
	return nil
}

func (n *naiveCleaner) active(file string) []*naiveVersion {
	var out []*naiveVersion
	for _, v := range n.files[file] {
		if !v.inTrash {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].t < out[j].t })
	return out
}

func (n *naiveCleaner) Add(now int64, file string, t, size int64) error {
	if err := n.clockErr(now); err != nil {
		return err
	}
	if now < 0 || file == "" || t < 0 || t > now || size < 1 {
		return ErrInvalid
	}
	for _, v := range n.files[file] {
		if v.t == t {
			return ErrDuplicate
		}
	}
	var sum int64
	for _, v := range n.files[file] {
		if !v.inTrash {
			sum += v.size
		}
	}
	if size > 1_000_000_000_000_000-sum {
		return ErrTooLarge
	}
	n.files[file] = append(n.files[file], &naiveVersion{t: t, size: size})
	n.hasNow = true
	n.lastNow = now
	return nil
}

func (n *naiveCleaner) setPin(file string, t int64, pinned bool) error {
	for _, v := range n.files[file] {
		if v.t == t && !v.inTrash {
			v.pinned = pinned
			return nil
		}
	}
	return ErrNoVersion
}

func (n *naiveCleaner) Pin(file string, t int64) error   { return n.setPin(file, t, true) }
func (n *naiveCleaner) Unpin(file string, t int64) error { return n.setPin(file, t, false) }

func (n *naiveCleaner) tierStep(age int64) int64 {
	for _, r := range n.rules {
		if age < r.Until {
			return r.Step
		}
	}
	return n.rules[len(n.rules)-1].Step
}

func (n *naiveCleaner) sortedFileNames() []string {
	names := make([]string, 0, len(n.files))
	for name := range n.files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (n *naiveCleaner) Clean(now int64) (CleanResult, error) {
	var res CleanResult
	if err := n.clockErr(now); err != nil {
		return res, err
	}
	if now < 0 {
		return res, ErrInvalid
	}

	// Trash purge: entries whose TTL elapsed are gone for good.
	for _, name := range n.sortedFileNames() {
		entries := n.files[name]
		sort.Slice(entries, func(i, j int) bool { return entries[i].t < entries[j].t })
		kept := entries[:0]
		for _, v := range entries {
			if v.inTrash && now-v.deletedAt >= n.trashTTL {
				res.Purged = append(res.Purged, Purge{File: name, T: v.t})
			} else {
				kept = append(kept, v)
			}
		}
		n.files[name] = kept
	}

	// Per-file retention, files in byte order.
	for _, name := range n.sortedFileNames() {
		vs := n.active(name)
		if len(vs) == 0 {
			continue
		}
		latest := vs[len(vs)-1].t
		maxAge := n.rules[len(n.rules)-1].Until
		var deleted []Deletion
		drop := func(v *naiveVersion, reason Reason) {
			v.inTrash = true
			v.deletedAt = now
			deleted = append(deleted, Deletion{File: name, T: v.t, Reason: reason})
		}

		// Step 1: Aged.
		var rem []*naiveVersion
		for _, v := range vs {
			if !v.pinned && v.t != latest && now-v.t >= maxAge {
				drop(v, ReasonAged)
			} else {
				rem = append(rem, v)
			}
		}

		// Step 2: Thinned.
		var kept []*naiveVersion
		var prev *naiveVersion
		for _, v := range rem {
			switch {
			case v.pinned || v.t == latest || prev == nil:
				kept = append(kept, v)
				prev = v
			case v.t-prev.t < n.tierStep(now-v.t):
				drop(v, ReasonThinned)
			default:
				kept = append(kept, v)
				prev = v
			}
		}

		// Step 3: count/byte limits.
		for {
			var count, bytes int64
			for _, v := range kept {
				count++
				bytes += v.size
			}
			if count <= n.maxCount && bytes <= n.maxBytes {
				break
			}
			idx := -1
			for i, v := range kept {
				if !v.pinned && v.t != latest {
					idx = i
					break
				}
			}
			if idx < 0 {
				break
			}
			reason := ReasonOverBytes
			if count > n.maxCount {
				reason = ReasonOverCount
			}
			v := kept[idx]
			kept = append(kept[:idx], kept[idx+1:]...)
			drop(v, reason)
		}

		sort.Slice(deleted, func(i, j int) bool { return deleted[i].T < deleted[j].T })
		res.Deleted = append(res.Deleted, deleted...)
	}

	n.hasNow = true
	n.lastNow = now
	return res, nil
}

func (n *naiveCleaner) Undelete(now int64, file string, t int64) error {
	if err := n.clockErr(now); err != nil {
		return err
	}
	if now < 0 {
		return ErrInvalid
	}
	for _, v := range n.files[file] {
		if v.t == t && v.inTrash && now-v.deletedAt < n.trashTTL {
			v.inTrash = false
			v.pinned = false
			n.hasNow = true
			n.lastNow = now
			return nil
		}
	}
	return ErrNoVersion
}

func (n *naiveCleaner) Versions(file string) []Version {
	var out []Version
	for _, v := range n.active(file) {
		out = append(out, Version{T: v.t, Size: v.size, Pinned: v.pinned})
	}
	return out
}

func (n *naiveCleaner) Totals(file string) (count int, bytes int64) {
	for _, v := range n.files[file] {
		if !v.inTrash {
			count++
			bytes += v.size
		}
	}
	return count, bytes
}

func (n *naiveCleaner) Trash(file string) []TrashEntry {
	var out []TrashEntry
	for _, v := range n.files[file] {
		if v.inTrash {
			out = append(out, TrashEntry{T: v.t, Size: v.size, DeletedAt: v.deletedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}

// cleanerAPI is satisfied by both Cleaner and naiveCleaner.
type cleanerAPI interface {
	Add(now int64, file string, t, size int64) error
	Pin(file string, t int64) error
	Unpin(file string, t int64) error
	Clean(now int64) (CleanResult, error)
	Undelete(now int64, file string, t int64) error
	Versions(file string) []Version
	Totals(file string) (count int, bytes int64)
	Trash(file string) []TrashEntry
}

type opKind int

const (
	opAdd opKind = iota
	opPin
	opUnpin
	opClean
	opUndelete
)

type op struct {
	kind opKind
	now  int64
	file string
	t    int64
	size int64
}

func (o op) String() string {
	switch o.kind {
	case opAdd:
		return fmt.Sprintf("Add(now=%d, file=%q, t=%d, size=%d)", o.now, o.file, o.t, o.size)
	case opPin:
		return fmt.Sprintf("Pin(file=%q, t=%d)", o.file, o.t)
	case opUnpin:
		return fmt.Sprintf("Unpin(file=%q, t=%d)", o.file, o.t)
	case opClean:
		return fmt.Sprintf("Clean(now=%d)", o.now)
	case opUndelete:
		return fmt.Sprintf("Undelete(now=%d, file=%q, t=%d)", o.now, o.file, o.t)
	}
	return "?"
}

type seqConfig struct {
	rules    []Rule
	maxCount int64
	maxBytes int64
	trashTTL int64
	files    []string
	ops      []op
}

func genConfig(rng *rand.Rand) seqConfig {
	nRules := 1 + rng.Intn(3)
	rules := make([]Rule, nRules)
	until := int64(1 + rng.Intn(40))
	for i := range rules {
		rules[i] = Rule{Until: until, Step: 1 + int64(rng.Intn(60))}
		until += 1 + int64(rng.Intn(200))
	}
	return seqConfig{
		rules:    rules,
		maxCount: 1 + int64(rng.Intn(8)),
		maxBytes: 1 + int64(rng.Intn(400)),
		trashTTL: 1 + int64(rng.Intn(40)),
		files:    []string{"a", "b", "c"},
	}
}

func genOps(rng *rand.Rand, cfg seqConfig) []op {
	nOps := 5 + rng.Intn(55)
	ops := make([]op, 0, nOps)
	now := int64(0)
	for i := 0; i < nOps; i++ {
		// Advance the clock most of the time; rarely stall or step back
		// (to exercise ErrClock paths).
		switch r := rng.Intn(100); {
		case r < 75:
			now += int64(rng.Intn(60))
		case r < 92:
			// keep now
		default:
			now -= int64(rng.Intn(30))
			if now < 0 {
				now = 0
			}
		}
		file := cfg.files[rng.Intn(len(cfg.files))]
		recentT := now - int64(rng.Intn(120))
		if recentT < 0 {
			recentT = 0
		}
		switch r := rng.Intn(100); {
		case r < 40:
			t := recentT
			size := int64(1 + rng.Intn(100))
			switch rng.Intn(20) {
			case 0:
				size = 0 // invalid
			case 1:
				t = now + 1 + int64(rng.Intn(5)) // invalid: t > now
			}
			if rng.Intn(50) == 0 {
				file = "" // invalid: empty file
			}
			ops = append(ops, op{kind: opAdd, now: now, file: file, t: t, size: size})
		case r < 52:
			ops = append(ops, op{kind: opPin, file: file, t: recentT})
		case r < 60:
			ops = append(ops, op{kind: opUnpin, file: file, t: recentT})
		case r < 78:
			ops = append(ops, op{kind: opClean, now: now})
		default:
			ops = append(ops, op{kind: opUndelete, now: now, file: file, t: recentT})
		}
	}
	return ops
}

// runSequence applies ops to c and returns a string trace of every call's
// result plus a full state dump after each step.
func runSequence(c cleanerAPI, cfg seqConfig) []string {
	var trace []string
	for _, o := range cfg.ops {
		var result string
		switch o.kind {
		case opAdd:
			result = fmt.Sprintf("err=%v", c.Add(o.now, o.file, o.t, o.size))
		case opPin:
			result = fmt.Sprintf("err=%v", c.Pin(o.file, o.t))
		case opUnpin:
			result = fmt.Sprintf("err=%v", c.Unpin(o.file, o.t))
		case opClean:
			res, err := c.Clean(o.now)
			result = fmt.Sprintf("err=%v deleted=%v purged=%v", err, res.Deleted, res.Purged)
		case opUndelete:
			result = fmt.Sprintf("err=%v", c.Undelete(o.now, o.file, o.t))
		}
		var state strings.Builder
		for _, f := range cfg.files {
			count, bytes := c.Totals(f)
			fmt.Fprintf(&state, " %s{v=%v totals=(%d,%d) trash=%v}", f, c.Versions(f), count, bytes, c.Trash(f))
		}
		trace = append(trace, fmt.Sprintf("%s -> %s |%s", o, result, state.String()))
	}
	return trace
}

func TestRandomSequencesAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		cfg := genConfig(rng)
		cfg.ops = genOps(rng, cfg)

		realCleaner, err := NewCleaner(cfg.rules, cfg.maxCount, cfg.maxBytes, cfg.trashTTL)
		if err != nil {
			t.Fatalf("seq %d: NewCleaner: %v", seq, err)
		}
		naive := newNaiveCleaner(cfg.rules, cfg.maxCount, cfg.maxBytes, cfg.trashTTL)

		gotTrace := runSequence(realCleaner, cfg)
		wantTrace := runSequence(naive, cfg)

		// Log inputs (op), outputs (results/state) and let the naive trace
		// serve as the step-by-step justification.
		for i, o := range cfg.ops {
			t.Logf("seq=%d step=%d input=%s", seq, i, o)
			t.Logf("seq=%d step=%d output(real)=%s", seq, i, gotTrace[i])
			t.Logf("seq=%d step=%d judgment(naive)=%s", seq, i, wantTrace[i])
		}

		for i := range wantTrace {
			if gotTrace[i] != wantTrace[i] {
				t.Fatalf("seq %d step %d mismatch\nconfig: rules=%v maxCount=%d maxBytes=%d trashTTL=%d\nop: %s\nreal : %s\nnaive: %s",
					seq, i, cfg.rules, cfg.maxCount, cfg.maxBytes, cfg.trashTTL,
					cfg.ops[i], gotTrace[i], wantTrace[i])
			}
		}

		// Replaying the same call sequence on a fresh Cleaner must reproduce
		// the exact same returns.
		replay, err := NewCleaner(cfg.rules, cfg.maxCount, cfg.maxBytes, cfg.trashTTL)
		if err != nil {
			t.Fatalf("seq %d: replay NewCleaner: %v", seq, err)
		}
		replayTrace := runSequence(replay, cfg)
		for i := range gotTrace {
			if gotTrace[i] != replayTrace[i] {
				t.Fatalf("seq %d step %d replay mismatch\nop: %s\nfirst : %s\nreplay: %s",
					seq, i, cfg.ops[i], gotTrace[i], replayTrace[i])
			}
		}
	}
}
