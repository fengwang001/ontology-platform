// Package logical joins physical lines of a Java .properties stream into
// logical lines, handling comments, blank lines and backslash continuations.
package logical

// Segment is one contiguous run of bytes copied from a single physical line.
type Segment struct {
	Data    []byte
	Phys    int  // 1-based physical line number
	Skip    int  // leading bytes stripped from the physical line (whitespace)
	HasTerm bool // true if this segment ends with a stripped trailing newline
}

// LogicalLine is one assembled logical line.
type LogicalLine struct {
	Data     []byte
	Comment  bool
	segments []Segment
}

// Splitter consumes a whole .properties document.
type Splitter struct {
	checks int64
}

// NewSplitter returns an empty Splitter.
func NewSplitter() *Splitter { return &Splitter{} }

// Checks reports the total number of bytes inspected.
func (s *Splitter) Checks() int64 { return s.checks }

// Split assembles logical lines. Skeleton.
func (s *Splitter) Split(input []byte) []LogicalLine {
	return nil
}

// Location maps a byte offset inside Data to a 1-based physical line/column.
func (l *LogicalLine) Location(offset int) (line, column int) {
	return 1, 1
}
