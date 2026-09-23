# Design

## Format

All integers are unsigned little-endian base-128 varints. Records are concatenated.

| Record | Byte layout |
|---|---|
| Header | `"ONTLZ77\n"` + version `1` varint |
| Literal | tag `0` + byte count + raw bytes |
| Match | tag `1` + distance + length |
| Flush | tag `2` |
| End | tag `3` + original length + FNV-1a 64 checksum |

## Derivations

1. **Write independence.** A write boundary contains no information about future bytes. `Write` only retains its bytes. Compression runs at `Flush` or `Close`, when every byte in that segment is available; the greedy matcher then sees one contiguous byte sequence. Thus 1-byte, 7-byte, and whole-buffer calls with identical flush offsets produce identical records. `Flush` must terminate the current greedy match because the decoder must reconstruct exactly the bytes received so far. The matcher state and window remain alive, so later matches may cross the flush boundary.
2. **Overlapping copies.** For distance `d < length l`, output position `p` may need byte `p-d` that is produced by the same match. A bulk source snapshot followed by one copy is wrong when it misses newly produced bytes. The decoder appends in increasing output order, reading the current ring for each emitted byte. This is still linear `O(l)`; asymptotic work equals the mandatory output size, so no algorithmic degradation occurs.
3. **Distance errors.** `distance > produced` names a reference to bytes that never existed; `distance <= produced` but `distance > window` names history the implementation cannot retain. They are therefore separate errors. A correctly initialized compressor only follows hash-chain candidates inside one fixed window, so it cannot emit the second kind. Parallel preset-dictionary bytes are not hidden from the decoder: every dictionary byte was ordinary output of the preceding block, so “already produced” remains the complete prefix of the same stream.
4. **Parallel determinism.** Block `i` receives only `data[i*blockSize:(i+1)*blockSize]` and the final at most `windowSize` bytes of block `i-1`. Its matcher has fixed capacity, fixed chain limit, fixed hash function, no clock input, and no shared mutable state. Therefore its records depend only on those bytes and not on goroutine scheduling. Blocks concatenate directly with no flush marker: dictionary bytes are already in decoded history, and the single header/footer describes the whole stream.

## Algorithms

The matcher uses 3-byte FNV-ish hash heads and one per-window-slot previous pointer. Each position examines at most the configured chain limit; advancing over a match inserts every covered position so chains remain dense. The encoder emits the first/lowest-distance longest greedy match and otherwise coalesces adjacent literals.
