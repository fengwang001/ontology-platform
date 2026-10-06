// Package settlement implements delayed settlement and a rolling reserve for
// payment merchants over an abstract business-day calendar.
//
// # Core model
//
// Every merchant configures:
//   - SettleDelayN: a transaction on business day b becomes settleable on the
//     settlement day t whose N-th business day on or before t is b;
//   - ReserveBps: the fraction (basis points) of a non-negative settleable net
//     that is retained into a new reserve batch, rounded UP;
//   - ReserveHorizonH: a batch retained on business day t is released on the
//     H-th business day strictly after t.
//
// On each settled business day t, in strict order, the engine:
//  1. adds the negative balance carried from the previous business day to the
//     transactions newly crossing the N-delayed boundary;
//  2. books the matured release (reserve batches whose release day is <= t);
//  3. if the settleable net is non-negative, retains ceil(net*bps/10000) into
//     a fresh batch and pays net - retain + release;
//  4. if it is negative, uses the release first and, if still negative,
//     consumes non-due batches from the oldest retention day to pull the
//     balance to zero; consumed reserve funds the deficit (the payout is 0),
//     and any remainder stays as the carried negative balance.
//
// Partially consumed batches release only their remaining balance when due;
// fully consumed batches are permanently gone. One business day never both
// retains and consumes.
//
// # Concurrency and determinism
//
// A single mutex makes every accepted operation atomic, so concurrent calls
// are equivalent to some serial interleaving. All operations carry a
// monotonic now; a rejected operation mutates nothing, including the clock.
// Replaying the same accepted operation stream reproduces identical payouts,
// batches, reserve balance and carried negative balance.
//
// # Complexity
//
// Per settled business day the work depends only on the transactions newly
// crossing the boundary, the batches maturing on that day, and the batches
// consumed that day — never on the merchant's full history. See DESIGN.md for
// the argument and the observable verification test.
package settlement
