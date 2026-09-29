# CSV dialect parser design

## 1. Empty lines and single-column empties

Choice: a truly empty line (zero bytes before its terminator: leading `\n`,
`\n\n`, ...) is SKIPPED, not a record. A row like `a,` or `,x` is a real
record with an unquoted empty cell; `""` is a real record with a quoted
empty cell. Treating blank lines as [empty] records would make the final
newline manufacture data and would make `a\n\n` ambiguous (a blank line
could not be distinguished from a genuine 1-column empty value after
re-serialization). Skipping keeps "final newline adds nothing" and keeps
the single-column empty value representable.

Writer consequence: in a 1-column table an empty cell of a real record MUST
be written `""`. Written bare, its row is an empty line that the parser
skips, so the record is lost. With >=2 columns an empty cell may stay empty
because a comma proves the record is non-blank. A quoted-empty always
serializes as `""` to preserve the quote mark.

## 2. Parallel split points (package par)

The buffer is cut at arbitrary offsets. A worker cannot know from its own
bytes whether it starts inside a quoted field, and a leading `\r` may be
the CR half of CRLF. Every non-first chunk is therefore scanned under TWO
entry hypotheses:
- O: first byte is outside quotes at field start; a leading `\r` is
  field-context pending CR.
- I: first byte is inside a quoted field.
Each run yields an end state {quote open/closed, pending-CR}. The true
entry is fixed by the previous chunk's committed last byte b and the next
chunk's first byte c:
- prior end inside quotes -> I;
- b==`"` (a closing quote, only outside quotes) -> O;
- b==`\r`: c==`\n` -> line end, O (pending CR consumed); c!=`\n` ->
  ErrBareCR at b (O);
- otherwise -> O.
The fixed end state then fixes the next chunk, cascading left to right.
Every byte is scanned at most twice (only non-first chunks), so total <=
2N+const regardless of quoted-field length.

Workers emit structural events only (cell start/append/end, terminator,
error) in local coordinates and never assemble rows. Cells straddling a
seam are concatenated by the stitcher when the chosen hypothesis changes.
Offsets are chunk-origin relative; the stitcher adds the origin. Local
record numbers get the count of terminators committed in earlier chunks;
field numbers restart at each terminator and are tracked by one shared
table assembler, so offsets/records/fields equal the streaming result.

## 3. Pending CR

Outside quotes, `\r` alone cannot be decided: followed by `\n` it is a
CRLF terminator, otherwise it is an illegal bare CR. State CRPending stores
no data and defers the decision; the next Feed byte decides, and Close with
CRPending reports ErrBareCR. At a par seam the CR is the previous chunk's
last byte and the next chunk's first byte decides it (see section 2). Inside
quotes `\r` is ordinary content, never pending, and is preserved verbatim.

## 4. Immediate limits

The field byte counter increments per admitted content byte. In quotes the
escape `""` is one quote character but TWO bytes; the byte limit counts raw
bytes and fires on the first byte that would exceed it, before any record
buffering. The field-count limit fires when a terminator would close a
record with too many cells. The record-count limit fires as a record
completes. A limit error is terminal: later Feed/Close return the same
error.

## State table

States S field-start, U unquoted, Q quoted, A quote-seen, C CR-pending.
Classes: `,` comma, `\n` LF, `\r` CR, `"` quote, o other.

| State | , | \n | \r | " | o |
|---|---|---|---|---|---|
| S | S empty cell | S end rec (blank=>skip) | C | Q quoted start | U append |
| U | S cell end | S rec end | C | ErrQuoteInUnquoted | U append |
| Q | Q append | Q append | Q append(keep CR) | A | Q append |
| A | S cell end | S rec end | C | Q append one " | ErrGarbageAfterQuote |
| C | ErrBareCR | S rec end CRLF | - | ErrBareCR | ErrBareCR |

EOF: empty S with no cell -> ok; U/A -> close cell then record; still in Q
-> ErrUnclosedQuote; C -> ErrBareCR.

Errors (distinguishable): ErrQuoteInUnquoted, ErrGarbageAfterQuote,
ErrUnclosedQuote, ErrColumnCount, ErrBareCR, ErrFieldTooLarge,
ErrTooManyFields, ErrTooManyRecords, ErrTerminal. Each carries Offset,
Record, Field (1-based; offset 0-based).

Canonical input: LF terminators only; no CR anywhere; minimal quoting - a
cell is quoted iff it contains `,` `"` `\r` `\n`, is a quoted empty, or is
an empty sole cell of a 1-column record.
