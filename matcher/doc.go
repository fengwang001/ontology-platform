// Package matcher is a single-instrument limit order matching engine with
// support for plain, iceberg and hidden orders.
//
// At each price level visible display batches form a FIFO queue. A plain
// order owns one batch; an iceberg order replenishes a fresh batch at the
// tail whenever its current batch is fully filled, losing time priority
// while remaining reachable by the same aggressor. Hidden orders trade only
// after the level's visible queue is empty, in sequence order. Because an
// iceberg only holds reserve while it also has a displayed batch, an empty
// visible queue is by construction equivalent to "all visible batches and
// iceberg reserve exhausted".
//
// All public methods are safe for concurrent use and are linearizable via a
// single RWMutex. See DESIGN.md for the full design and the differential
// tests against the independent naive reference implementation.
package matcher
