// Package cell represents one CSV field value and its raw byte span.
package cell

// Cell is one field of one record.
//
// Value is the decoded field content: "" escapes collapse to one quote and
// quoted \r\n is preserved verbatim (never normalized to \n). Quoted reports
// whether the field was written with surrounding quotes, so an unquoted empty
// field differs from a quoted empty one ("" == Quoted with empty Value).
// Start and End are absolute byte offsets of the field's raw span, half-open;
// for an empty unquoted field Start == End at the position of the delimiter.
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}
