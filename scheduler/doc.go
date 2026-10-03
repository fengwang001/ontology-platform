// Package scheduler implements a deterministic weakly-hard real-time
// distance-priority scheduler on one preemptive core.
//
// Each task keeps a window of its latest k job outcomes. The oldest value is
// on the left and the newest value is on the right; 1 means met and 0 means
// missed. Distance s is the minimum number of consecutive zero outcomes that
// can be appended at the right before retaining only the latest k values leaves
// fewer than m met outcomes. If the current window already contains fewer than
// m ones, s is zero. For (m,k)=(2,3), windows 111, 110, 101, 011, and 100 have
// distances 2, 1, 1, 2, and 0. When m=k, s is therefore one for 11...1 and
// zero otherwise.
//
// Every tick [t,t+1) performs, in order:
//  1. every active job whose remaining execution is greater than d-t is marked
//     missed, removed immediately, and appends 0; this includes d=t with work
//     remaining;
//  2. jobs for which t >= Phi and (t-Phi) mod T == 0 are released with C
//     remaining ticks and absolute deadline t+T;
//  3. among available jobs, the minimum (s, d, ID-by-byte-wise-order) runs for
//     one tick, using the distances after misses and releases in this tick;
//  4. if that job reaches zero remaining execution, it appends 1 and is
//     removed, so completion at t+1=d is still timely.
//
// Every appended outcome independently increments DynamicFailures when the
// resulting window has fewer than m ones. All public methods acquire the
// scheduler mutex, so concurrent calls are equivalent to some serial order.
package scheduler
