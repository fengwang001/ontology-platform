// Package splitdeque provides a split double-ended queue with a private
// region for the owner and a shared region for stealers.
//
// Elements are int64 values occupying consecutive logical indices in push
// order. The three boundaries t <= s <= b split the ring buffer into:
//
//   - released/empty: indices before t, already stolen;
//   - shared region S: indices [t, s), stealable by any thief;
//   - private region P: indices [s, b), owned exclusively by the owner.
//
// Initially t = s = b = 0. |S| = s-t never exceeds Sm and |P| = b-s.
// The total occupancy b-t never exceeds Cap. Releasing and reclaiming
// only move the split point s; no element bytes are ever moved
// (see MoveCounts).
//
// Steal requests. A Steal that receives fewer than m elements (k < m,
// including k = 0) records a pending request: fl becomes true (if it was
// already true it stays true), the deficit dm becomes max(dm, m-k) and
// the age ag resets to 0. A fully satisfied steal (k == m) leaves fl,
// ag and dm untouched.
//
// Release check. Push (after appending) and Pop (before removing) run the
// release check only when fl is true:
//
//	r = min(max(floor(|P|/2), dm), Sm-|S|, |P|-Rv), clamped at 0
//
// If r >= 1 the r oldest private elements move into the shared region
// (s += r) and fl/ag/dm are cleared. Otherwise ag is incremented; when
// ag reaches F the request expires and fl/ag/dm are cleared. Clearing
// happens either way on a successful release or on age expiry, so dm is
// reset in both cases. An empty Pop still runs the check and therefore
// still ages a pending request.
//
// Reclaim. Pop removes from the private back (index b-1). When P is
// empty but S is not, it first moves g = ceil(|S|/2) newest shared
// elements into P (s -= g), then pops index b-1.
//
// Stats() reports pushed, popped and stolen element counts, the number
// of steals with k < m (Misses), release counts and reclaim counts.
// Pushed always equals Popped + Stolen + (b-t).
//
// All operations take one internal lock, so concurrent calls are
// equivalent to some serial order and invariants hold at every instant.
package splitdeque
