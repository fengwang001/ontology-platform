package initsession

// Stats exposes internal counters that let tests verify the complexity
// claims: no per-referencer re-expansion of function bodies, and no full
// rescan of remaining units each round.
//
// Every Solve builds its own Stats (Solves never mutate the session), so
// concurrent callers observe independent counters.
type Stats struct {
	// FunctionClosuresComputed is the number of distinct function
	// transitive closures actually computed (memoized; recursion is
	// handled with in-flight states).
	FunctionClosuresComputed int
	// FunctionRefExpansions counts transitions from a function reference
	// into the memo table (hit or miss counted separately below).
	FunctionRefExpansions int
	// FunctionCacheHits counts memo-table hits while computing closures.
	FunctionCacheHits int
	// RefEdgesTraversed counts identifier references examined while
	// building closures and unit dependency sets (each stored reference
	// at most once per consumer, and closures are shared).
	RefEdgesTraversed int
	// ReadyChecks counts individual unit "are deps ready" evaluations
	// performed by the scheduler. A naive round-based rescan performs
	// O(remaining units) checks per round; the heap scheduler performs
	// one check per reverse-edge notification plus one per ready pop.
	ReadyChecks int
	// ReverseNotifications counts dependency-completed notifications
	// delivered to waiting units.
	ReverseNotifications int
	// Units and Functions are the snapshot sizes used by the solve.
	Units     int
	Functions int
	// Refs is the total number of stored (deduplicated) references
	// across all accepted declarations.
	Refs int
}
