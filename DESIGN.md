Design: streaming UTF-8 <-> UTF-16 with replacement

## 1. Invalid-unit segmentation (derived from the 7 samples)

Rule (byte-by-byte, one pass): read a lead byte L and immediately demand the
next `n-1` bytes (n = length L demands). A demanded byte that is absent at EOF,
or whose value is outside the legal range for its position, makes the *longest
still-viable prefix already consumed* one invalid unit; bytes after the first
bad one are re-decoded as fresh lead bytes.
- `F0 90 80 41 -> FFFD 41`: F0 demands 90..BF at pos 2 (`90` ok), then 80..BF
  at pos 3 (`80` ok), then a continuation at pos 4 — `41` is not one — so the
  triple `F0 90 80` is one unit, `41` restarts as 'A'.
- `E0 80 80`: after E0 only A0..BF is legal; `80` dies at pos 2, so unit `E0`;
  restart on `80`, then `80` => three units.
- `ED A0 80`: after ED only 80..9F legal; `A0` bad => unit `ED`; then `A0`,
  `80` => three.
- `C0 AF`: C0 never valid => unit C0; AF standalone => second.
- `F4 90 80 80`: after F4 only 80..8F; `90` bad => F4, then three stray
  continuations => four.
- `E2 82` + EOF: E2 accepts any 80..BF at pos 2; `82` fine, third missing =>
  one unit `E2 82` (one FFFD, not two).
- `80 80`: stray continuations, each its own unit => two.
Distinction F0-case vs E0-case: restricted second-byte ranges kill the prefix
on the second byte (lead alone is the unit); ordinary leads accept any
continuation and the death is found later (whole prefix is the unit).

Second-byte ranges / swallow count when the 2nd byte is illegal:

| lead range   | len | 2nd ok  | swallow |
|--------------|-----|---------|---------|
| C2..DF       | 2   | 80..BF  | 1       |
| E0           | 3   | A0..BF  | 1       |
| E1..EC       | 3   | 80..BF  | 1       |
| ED           | 3   | 80..9F  | 1       |
| EE..EF       | 3   | 80..BF  | 1       |
| F0           | 4   | 90..BF  | 1       |
| F1..F3       | 4   | 80..BF  | 1       |
| F4           | 4   | 80..8F  | 1       |
| 80..BF       | -   | stray   | 1       |
| C0 C1 F5..FF | -   | invalid | 1       |

Later demanded positions always require 80..BF; a bad byte there leaves the
full prefix as one unit, the bad byte restarts. EOF truncation: the buffered
prefix is one unit.

## 2. Buffer bound and immediate refusal

Max sequence is 4 bytes => at most 3 buffered. A failed 2nd-byte range refuses
the lead immediately; a bad/missing 3rd or 4th byte is found while inspecting
the next byte, so the prefix unit is emitted without waiting for EOF. EOF
waiting is needed only for a still-valid prefix at stream end.

## 3. par alignment

At each interior cut, the right segment start is aligned: rewind over up to 3
preceding continuation bytes; if the byte before them is a lead demanding
exactly that many continuations, start there (overlap), else at the first
non-continuation. Each segment decodes independently; a non-final segment
discards its trailing incomplete-prefix tail (everything past its last clean
scalar/invalid-unit boundary). `C3|80 80`: right side rewinds to C3 and joins
`C3 80`; left side's dangling C3 tail is dropped -> pair counted once. An
illegal lead (80, C0, F5..) never joins, so unit counts match single-stream.
Rewind touches <=3 bytes per cut: total O(n + 3(K-1)); global offsets use the
absolute start offset of each (aligned) segment.

## 4. Limit breakpoint

Write emits only complete scalars; the lead byte of the next scalar is the
cutoff. Carried bytes and any unreached suffix of p are exposed by
ResumeBytes() and are NOT in the returned n. Feed them to a fresh instance;
concatenation is byte-identical. UTF-16 never emits half a surrogate pair.

## 5. Check-count accounting

Every byte is classified at most twice (arrival, then prefix completion or
failure); carried bytes are never reclassified => <=2n at any chunk size. par
adds a bounded <=3-byte rewind per cut: n + 3(K-1).

## 6. Notes

stream.Transcoder is NOT concurrency-safe; par uses one instance per goroutine.
BOM recognized only at absolute offset 0; later U+FEFF is plain data; a split
BOM (EF|BB|BF, FE|FF) reassembles through the carry. Errors ErrInvalidByte
(offset,len), ErrTruncated(offset), ErrOutputLimit, ErrClosed are distinct.
