// Package tictoc implements a TicToc-style data-driven optimistic
// transaction validator.
//
// Each tuple carries a value v, a write timestamp w and a read timestamp r
// (invariant: w <= r), plus at most one lock holder. Transactions buffer all
// writes; the commit timestamp is derived from the live read/write sets only
// at Prepare time:
//
//	c = max( max over write keys of tuple.r + 1,
//	         max over read records of observed w )
//
// with c = 0 for an empty read/write set, so a read-only transaction that only
// ever saw timestamp 0 commits before later writers.
//
// Read-set validation extends, but never shrinks, read timestamps: when a
// record (k,w0,r0) has r0 < c, validation passes without touching the tuple
// only if r0 >= c; otherwise it requires tuple.w == w0, then passes when
// tuple.r >= c or the tuple is locked by this transaction, and otherwise
// extends tuple.r to c when unlocked. A lock held by another transaction at
// that point aborts with ErrExtensionBlocked.
//
// Any Prepare abort (lock conflict in step 1, version change, or blocked
// extension in step 3) rolls back every lock acquired and every read
// timestamp extended during that call, leaving tuples field-identical to
// before the call. Successful extensions persist; Finish installs each
// buffered value with w = r = c and releases the locks.
//
// All operations are serialized on a single mutex, hence concurrent calls are
// equivalent to some serial order, and the whole API has no random inputs, so
// replaying the same call sequence reproduces identical results and states.
package tictoc
