package inline

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Logger receives one structured line per input, decision basis and output.
// Implementations must be safe for concurrent use if tasks run concurrently.
type Logger interface {
	Logf(format string, args ...any)
}

// logWriter adapts an io.Writer (e.g. os.Stderr) to Logger, guarded externally
// if shared.
type logWriter struct{ w io.Writer }

func (l logWriter) Logf(format string, args ...any) {
	fmt.Fprint(l.w, fmt.Sprintf(format, args...))
	if !strings.HasSuffix(format, "\n") {
		fmt.Fprintln(l.w)
	}
}

// instance is one concrete call site under consideration. A copied site is a
// new instance with its own path, heat and chain; the original site identity
// is preserved by Path.
type instance struct {
	owner  string // lexical owner of the body holding the site
	callee string
	heat   float64
	// Path identifies the site: indices taken from the root down. Sibling
	// branches diverge at the last index.
	path []int
	// ch is the expansion path active when this site is examined.
	ch *chain
}

// Decide runs one deterministic task for root and returns its report.
// The registry is frozen for the task's duration.
func (r *Registry) Decide(root string, cfg Config, log Logger) (*Report, error) {
	reports, err := r.decide([]string{root}, cfg, log)
	if err != nil {
		return nil, err
	}
	return reports[root], nil
}

// DecideAll runs one deterministic task for every registered function.
// Root analyses are independent; they share only the immutable snapshot, so
// they may execute concurrently without affecting each other's results.
func (r *Registry) DecideAll(cfg Config, log Logger) (map[string]*Report, error) {
	snap := r.beginTask(cfg)
	defer r.endTask(snap)

	roots := append([]string(nil), snap.order...)
	if log != nil {
		log.Logf("input: config=%+v funcs=%v", snap.cfg, roots)
	}
	return runRoots(snap, roots, log, true), nil
}

func (r *Registry) decide(roots []string, cfg Config, log Logger) (map[string]*Report, error) {
	snap := r.beginTask(cfg)
	defer r.endTask(snap)

	for _, root := range roots {
		if _, ok := snap.funcs[root]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownFunc, root)
		}
	}
	if log != nil {
		log.Logf("input: config=%+v roots=%v funcs=%v", snap.cfg, roots, snap.order)
	}
	return runRoots(snap, roots, log, true), nil
}

// runRoots analyses every root against the same immutable snapshot.
func runRoots(snap *snapshot, roots []string, log Logger, concurrent bool) map[string]*Report {
	results := make(map[string]*Report, len(roots))
	if concurrent {
		type res struct {
			name string
			rep  *Report
		}
		done := make(chan res, len(roots))
		for _, root := range roots {
			go func(root string) {
				done <- res{root, analyse(snap, root, log)}
			}(root)
		}
		for range roots {
			x := <-done
			results[x.name] = x.rep
		}
		return results
	}
	for _, root := range roots {
		results[root] = analyse(snap, root, log)
	}
	return results
}

// analyse is the layered expansion for one root. It owns only stack-local
// state and the immutable snapshot, hence independent from other tasks.
func analyse(snap *snapshot, root string, log Logger) *Report {
	rootFn := snap.funcs[root]
	acc := newAccount(rootFn.Size, snap.cfg)
	rep := &Report{
		Function:    root,
		InitialSize: rootFn.Size,
	}

	pending := make([]instance, 0, len(rootFn.Sites))
	for i, s := range rootFn.Sites {
		pending = append(pending, instance{
			owner: root, callee: s.Callee, heat: s.Heat,
			path: []int{i}, ch: newChain(root),
		})
	}

	deepest := newChain(root)
	maxDepth := 0

	for len(pending) > 0 {
		idx := pickSite(pending)
		site := pending[idx]
		pending = append(pending[:idx], pending[idx+1:]...)

		if d := site.ch.depth(); d > maxDepth {
			maxDepth = d
			deepest = site.ch
		}

		fate, reason := judge(snap, acc, site)
		rep.recordFate(site, fate, reason)

		if log != nil {
			log.Logf("basis %s site%v owner=%s callee=%s heat=%g chain=%v current=%d : %s %s",
				root, site.path, site.owner, site.callee, site.heat,
				site.ch.snapshot(), acc.current, fateLabel(fate), reasonLabel(reason))
		}

		if fate == FateRejected {
			continue
		}

		callee := snap.funcs[site.callee]
		acc.apply(callee.Size)

		// The copied body belongs to the callee; its sites join the undecided
		// pool and are re-ranked together with everything still pending. Each
		// child gets its own chain copy, so rejected or later-accepted siblings
		// never share (nor inherit) one another's frame counts.
		childChain := site.ch.withAppended(site.callee)
		if childChain.depth() > maxDepth {
			maxDepth = childChain.depth()
			deepest = childChain
		}
		ratio := site.heat / snap.base[site.callee]
		for j, child := range callee.Sites {
			childPath := append(append([]int(nil), site.path...), j)
			pending = append(pending, instance{
				owner:  site.callee,
				callee: child.Callee,
				heat:   child.Heat * ratio,
				path:   childPath,
				ch:     childChain,
			})
		}
	}

	acc.accountInto(rep)
	rep.DeepestPath = append([]string(nil), deepest.snapshot()...)
	if log != nil {
		log.Logf("output: root=%s initial=%d final=%d inlined=%d rejected=%d depth=%d path=%v",
			root, rep.InitialSize, rep.FinalSize,
			rep.count(FateInlined), rep.count(FateRejected), maxDepth, rep.DeepestPath)
	}
	return rep
}

// pickSite returns the index of the next site: highest heat, then earliest
// lexical position (path compared lexicographically). Floating-point order is
// the only ordering used; equal heats are defined by identical float values.
func pickSite(pending []instance) int {
	best := 0
	for i := 1; i < len(pending); i++ {
		if before(pending[i], pending[best]) {
			best = i
		}
	}
	return best
}

func before(a, b instance) bool {
	if a.heat != b.heat {
		return a.heat > b.heat
	}
	return pathLess(a.path, b.path)
}

func pathLess(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// judge applies the fixed reason priority and returns the first cause that
// holds; only then is the budget consulted.
func judge(snap *snapshot, acc *Account, site instance) (Fate, Reason) {
	callee, defined := snap.funcs[site.callee]
	if !defined {
		return FateRejected, ReasonUndefinedCallee
	}
	if callee.Flags == FlagNoInline {
		return FateRejected, ReasonNoInline
	}
	if site.ch.directRecursion(site.callee) {
		return FateRejected, ReasonDirectRecursion
	}
	if callee.Uninlinable {
		return FateRejected, ReasonUninlinableStructure
	}
	if site.ch.occurrencesAfterInlining(site.callee) > snap.cfg.MaxChainOccurrences {
		return FateRejected, ReasonChainExceeded
	}
	if callee.Flags == FlagAlwaysInline {
		return FateInlined, -1
	}
	if !acc.allows(callee.Size) {
		return FateRejected, ReasonBudget
	}
	return FateInlined, -1
}

func fateLabel(f Fate) string {
	if f == FateInlined {
		return "INLINED"
	}
	return "REJECTED"
}

func reasonLabel(r Reason) string {
	if r == -1 {
		return "-"
	}
	return r.String()
}

// sortedReports is a small deterministic helper for external consumers.
func sortedReports(m map[string]*Report) []*Report {
	out := make([]*Report, 0, len(m))
	for _, rep := range m {
		out = append(out, rep)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Function < out[j].Function })
	return out
}
