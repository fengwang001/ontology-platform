/*
Package message parses and re-serialises the custom wire format
without losing anything the parser does not understand.

# Forward compatibility

When an old service receives a message produced by a newer service,
unknown field numbers must survive being parsed and forwarded. Parse
therefore records every field, known or not, and Marshal emits them
again. See package unknown for the retention container.

# Ordering rule

The ordering rule is derived, not chosen freely. The byte-for-byte
round-trip invariant states that parsing a legal message and
marshalling it again must reproduce the input exactly. Fields in the
input form one sequence in which known and unknown fields may be
arbitrarily interleaved, and one field number may occur several
times. Therefore:

  - every field keeps the slot it occupied in the input; there is no
    grouping by "known first, unknown last" and no sorting;
  - repeated occurrences, including repeated unknown fields, stay in
    their original relative order and are never deduplicated or
    merged;
  - modifying a known field changes only that slot's payload;
  - deleting a known field removes just its slot, so the surviving
    fields retain the relative order they already had.

Any other rule would reorder bytes relative to the input and violate
the round-trip invariant.

# Failure handling

Parse builds into a private value and returns nil with the error on
any failure, so no half-parsed message can reach the caller. Errors
carry the byte offset (wire.ParseError) and distinguish the five

	syntax failures plus limit and depth failures. Payload bytes are
	copied during parsing; parsed structures never alias the input
	buffer, and Marshal never mutates the Msg, so it is safe to
	marshal the same value repeatedly or concurrently.
*/
package message
