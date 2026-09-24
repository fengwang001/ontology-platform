 # DESIGN
 
 ## 1. End-of-line finalize policy
 
 Finalize runs on the byte sequence after CR/WS normalization.
 - Keep: output unchanged (missing final newline stays missing).
 - EnsureOne: empty input => ""; otherwise guarantee exactly one final
   '\n' (add one if missing). "" for empty preserves the fixed point of
   emptiness; "\n" is also idempotent but invents content for an empty
   file, so we pick "".
 - TrimBlank: delete every trailing blank line; if at least one content
   line remains, end it with one '\n'; empty/whitespace-only => "".
 All three are idempotent: outputs have no CR, no trailing WS, and the
 final-line shape is already a fixed point.
 
 | input     | Keep    | EnsureOne | TrimBlank |
 |-----------|---------|-----------|-----------|
 | ""        | ""      | ""        | ""        |
 | "\n"      | "\n"    | "\n"      | ""        |
 | "\n\n"    | "\n\n"  | "\n"      | ""        |
 | "  \r\n"  | "\n"    | "\n"      | ""        |
 
 ## 2. ToOut of deleted bytes
 
 Every transform op maps orig range [a0,a1) to out range [b0,b1): copy
 (equal lengths), delete (out length 0), or an inserted '\n' after a
 lone CR (orig 1 / out 1 anchored at the CR). Boundary ToOut(a1)=b1.
 A deleted orig position i (CR of CRLF, or trailing WS) inside
 [a0,a1) maps to the left boundary b0; ToOrig(b0)=a0 (left-orig
 preference), hence ToOut(ToOrig(b0))=b0 and round trip holds. Both
 maps are monotone because anchors only move forward. A cursor inside
 removed WS or on the removed CR lands just BEFORE the emitted '\n'.
 
 ## 3. WS buffer overflow
 
 Force-emitting pending WS would rewrite already-anchored output and,
 if a line ending follows, violate idempotence (rule 5) and split
 invariance (rule 4, an artificial cut). We REJECT with ErrWSLimit at
 the orig offset of the first byte beyond the limit; emitted output is
 retained, the instance becomes terminal (later writes => ErrClosed;
 Close still finalizes). Output overflow likewise rejects with
 ErrOutLimit before the byte is written.
 
 ## 4. par stitching
 
 Each segment runs an independent segment normalizer (Keep policy; EOF
 only yields pending state, never forces a newline) producing span.Op
 values tagged by segment, plus end state (pending CR / pending WS
 offsets). Ops are concatenated, coordinates translated by prefix
 sums, then corrected incrementally while carrying left trailing WS:
 - CR|LF: drop the inserted '\n' op at the boundary.
 - WS run crossing a cut: fate decided by the first non-WS byte after
   the carried run; \r or \n => delete, otherwise keep; WS-only file
   => keep.
 Per boundary only the left segment's final CR op and one contiguous
 cross-cut WS run are touched, so per-boundary work is bounded; total
 work is O(K + cross-run bytes) and the op stream is cut-independent.
 The configured final policy is then applied globally, exactly as the
 single-thread path.
 
 Single norm instances are NOT safe for concurrent use; par uses one
 independent normalizer per goroutine.
