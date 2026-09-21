# ontology — RLE bitmap set

A run-length encoded set over the `uint32` universe, in pure Go (standard
library only, in-memory state). Supports `Set` / `Clear` / `Contains`,
compressed-domain `Union` / `Intersect` / `Difference`, and O(runs)
statistics (`Count` / `Min` / `Max`). All types are safe for concurrent
use via an internal `sync.RWMutex`.

## Canonical encoding

A set is a list of alternating zero/one runs. The canonical form —
maintained after every mutation and every compressed-domain operation —
guarantees one unique byte sequence per set:

- adjacent runs always alternate in value (same-value runs are merged);
- no run has length 0;
- no trailing zero run is kept (the last run, if any, is a one-run).

`Verify()` checks exactly these three invariants.

Byte layout of `Bytes()`:

```
uvarint  N                       number of runs
N x (byte flag, uvarint length)  flag 0x01 = one-run, 0x00 = zero-run
```

- **Empty set**: `N = 0`, encoded as the single byte `0x00` — the
  deterministic, shortest possible encoding.
- **Huge runs**: lengths are uvarints over `uint64`, so a single run of
  length 2^32 (the whole universe, e.g. after `SetRange(0, math.MaxUint32)`)
  encodes directly. Internally all positions and lengths are `uint64`,
  so `end = start + length` never overflows even at `math.MaxUint32`.

## Compressed-domain operations

`Union` / `Intersect` / `Difference` walk both run lists with two cursors
and emit one output segment per maximal interval on which both inputs are
constant. They never expand to a per-bit form. Each returns an `OpStats`
whose `Steps` field reports how many run segments the cursors advanced
through — bounded by the input run counts, not the bit counts.

## Statistics

`CountEx` / `MinEx` / `MaxEx` return the number of runs inspected alongside
the result, demonstrating the O(runs) cost. `Max` is O(1): the last run is
always a one-run in canonical form.

## Layout

- `bitmap.go` — core type, normalization, prefix-sum index
- `mutate.go` — `Set` / `Clear` / `Contains` / `SetRange`
- `encode.go` — `Bytes` / `Verify`
- `ops.go` — compressed-domain `Union` / `Intersect` / `Difference`
- `stats.go` — `Count` / `Min` / `Max` (+ `Ex` instrumentation)
- `cmd/demo` — runnable demonstration (`go run ./cmd/demo`)

## Checks

```
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
go run ./cmd/demo
```
