# DESIGN — streaming UTF-8 <-> UTF-16 with replacement

## 1. Illegal-unit segmentation (derived from the seven samples)
Rule = single-scalar recovery, WHATWG-style:
- (a) lead byte b gives expected length L (2..4); invalid leads 80..BF,C0,C1,F5..FF are a length-1 bad unit immediately.
- (b) the SECOND byte must fall in the lead-specific range R in the table below. A byte outside R is itself a length-1 bad unit and is RE-PARSED as a new lead; nothing after it is swallowed.
- (c) after a valid second byte, a later non-continuation makes the whole pending prefix ONE bad unit; the new byte is re-parsed. At EOF a pending prefix is one bad unit (replacement) / truncation error (strict).

Samples: `F0 90 80 41`: second 90 is in F0's range, third 80 is a bad later continuation => prefix F0 90 80 = one unit, 41 re-parsed. `E0 80 80`: 80 outside E0 range => len1; then two lone continuations => three. `ED A0 80`: A0 outside ED [80..9F] => len1 + two lone. `C0 AF`: two invalid leads. `F4 90 80 80`: 90 outside F4 [80..8F] => four len1. `E2 82`+EOF: valid second byte, tail missing => one unit for the whole prefix (not two). `80 80`: two lone continuations.

| lead class | second-byte range | bad second: swallow |
|---|---|---|
| C2..DF | 80..BF | 1, re-parse second |
| E0 | A0..BF | 1 |
| E1..EC | 80..BF | 1 |
| ED | 80..9F | 1 |
| EE..EF | 80..BF | 1 |
| F0 | 90..BF | 1 |
| F1..F3 | 80..BF | 1 |
| F4 | 80..8F | 1 |
| 80..BF (lone) | - | 1 |
| C0 C1 F5..FF | - | 1 |

Truncated/tail-bad prefix (EOF or non-continuation after a valid second byte): swallow = whole pending length 2..4.

## 2. Buffer bound and early rejection
At most one in-progress prefix is buffered: <=3 bytes, so the cache hard cap is 3. A lone lead never emits FFFD (split-safe). The second byte is the deciding byte: outside R kills the prefix immediately — emit a len-1 bad unit for that byte, never wait for EOF. After a valid second byte only EOF needs special handling (Close); a non-continuation is also decided immediately.

## 3. par cut alignment
At cut c back up over at most 3 trailing 80..BF bytes; if the preceding byte is a lead whose expected unit reaches past c, the segment starts at that lead (boundary b<=c), otherwise at c. Boundaries are clamped monotone: a segment may then begin mid-unit, and its decoder emits exactly the same len-1 bad units a sequential decoder would, so the total bad-unit count is identical. Only the final segment is closed with EOF; interior segments end on a boundary and never report truncation. Offset fields are seeded with each segment's start so strict errors report global offsets. Example `C3 | 80 80`: backup finds lead C3 reaching past the cut, so the next segment starts at C3 and the previous ends before it — the scalar (or bad unit) is never split.

## 4. Limit breakpoint (rule 9)
Before emitting each complete scalar check len(out)+encLen <= MaxOut; otherwise stop at that scalar boundary with ErrLimit. Write's n counts only bytes of fully emitted scalars; bytes held in the pending prefix are NOT consumed — retried on the next Write (or handed to a fresh instance on resume). Concatenating the outputs of successive instances equals the unlimited run. UTF-16 surrogate pairs are emitted atomically (no half pair).

## Invariants
Stats, any split: validBytes + badBytes + bomBytes == consumed input bytes. Each input byte is examined <=2 times (once as data, at most once as a held/re-parsed byte); par adds only O(K) boundary lookback bytes, so checks <= n + 8*const for K<=8. A stream-initial BOM selects UTF-16 endianness and is stripped or passed per config; mid-stream FEFF is an ordinary scalar. A single `stream.Transcoder` is not safe for concurrent use.
