package layout

import "sync"

type EventType string

const (
	EventIndent    EventType = "INDENT"
	EventDedent    EventType = "DEDENT"
	EventNewline   EventType = "NEWLINE"
	EventEndMarker EventType = "ENDMARKER"
)

type Event struct {
	Type EventType
}

type ErrorReason string

const (
	ReasonClosed           ErrorReason = "closed"
	ReasonLineTooLong      ErrorReason = "line too long"
	ReasonInconsistentTab  ErrorReason = "inconsistent tabs"
	ReasonBadDedent        ErrorReason = "bad dedent"
	ReasonExpectedIndent   ErrorReason = "expected indented block"
	ReasonUnexpectedIndent ErrorReason = "unexpected indent"
	ReasonIndentTooDeep    ErrorReason = "indentation too deep"
	ReasonDanglingBranch   ErrorReason = "dangling branch"
	ReasonString           ErrorReason = "unterminated string"
	ReasonBracket          ErrorReason = "mismatched bracket"
	ReasonBracketTooDeep   ErrorReason = "brackets too deep"
	ReasonIncompleteInput  ErrorReason = "incomplete input"
	ReasonMissingBlock     ErrorReason = "missing indented block"
	ReasonInvalidArgument  ErrorReason = "invalid argument"
)

type Error struct {
	Reason ErrorReason
}

func (err *Error) Error() string {
	return string(err.Reason)
}

type Level struct {
	Column    int
	AltColumn int
	HeadWord  string
}

type Snapshot struct {
	IndentStack      []Level
	BracketStack     []byte
	LogicalOpen      bool
	LastValidChar    byte
	HasLastValidChar bool
	ExpectBlock      bool
	Closed           bool
}

type Layout struct {
	mu           sync.Mutex
	indentLimit  int
	bracketLimit int
	levels       []Level
	brackets     []byte
	logicalOpen  bool
	continuation bool
	lastValid    byte
	hasLastValid bool
	expectBlock  bool
	closed       bool
	scannedBytes int
	indentEvents int
	dedentEvents int
}

type indentRelationKind int

const (
	relationSame indentRelationKind = iota
	relationIndent
	relationDedent
)

type indentRelation struct {
	kind        indentRelationKind
	dedentCount int
	reason      ErrorReason
}

func New(indentLimit int, bracketLimit int) (*Layout, error) {
	if indentLimit < 1 || indentLimit > 100 || bracketLimit < 1 || bracketLimit > 200 {
		return nil, &Error{Reason: ReasonInvalidArgument}
	}
	return &Layout{
		indentLimit:  indentLimit,
		bracketLimit: bracketLimit,
		levels:       []Level{{}},
	}, nil
}

func (state *Layout) Feed(line []byte) ([]Event, error) {
	state.mu.Lock()
	defer state.mu.Unlock()

	if state.closed {
		return nil, &Error{Reason: ReasonClosed}
	}
	if len(line) > 10000 {
		return nil, &Error{Reason: ReasonLineTooLong}
	}

	if len(state.brackets) > 0 || state.continuation {
		return state.feedContinuation(line)
	}
	return state.feedLogicalStart(line)
}

func (state *Layout) Close() ([]Event, error) {
	state.mu.Lock()
	defer state.mu.Unlock()

	if state.closed {
		return nil, &Error{Reason: ReasonClosed}
	}
	if len(state.brackets) > 0 || state.continuation {
		return nil, &Error{ReasonIncompleteInput}
	}
	if state.expectBlock {
		return nil, &Error{ReasonMissingBlock}
	}

	dedentCount := len(state.levels) - 1
	events := make([]Event, 0, dedentCount+1)
	for range state.levels[1:] {
		events = append(events, Event{Type: EventDedent})
	}
	events = append(events, Event{Type: EventEndMarker})
	state.dedentEvents += dedentCount
	state.levels = state.levels[:1]
	state.closed = true
	return events, nil
}

func (state *Layout) Snapshot() Snapshot {
	state.mu.Lock()
	defer state.mu.Unlock()

	return Snapshot{
		IndentStack:      append([]Level(nil), state.levels...),
		BracketStack:     append([]byte(nil), state.brackets...),
		LogicalOpen:      state.logicalOpen,
		LastValidChar:    state.lastValid,
		HasLastValidChar: state.hasLastValid,
		ExpectBlock:      state.expectBlock,
		Closed:           state.closed,
	}
}

func (state *Layout) ScannedBytes() int {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.scannedBytes
}

func (state *Layout) feedContinuation(line []byte) ([]Event, error) {
	scanned := scanLine(line, state.brackets, state.bracketLimit)
	if scanned.reason != "" {
		return nil, &Error{Reason: scanned.reason}
	}

	state.scannedBytes += len(line)
	state.brackets = scanned.brackets
	state.continuation = false
	if scanned.hasLastValid && (!scanned.continuation || len(scanned.brackets) > 0) {
		state.lastValid = scanned.lastValid
		state.hasLastValid = true
	}
	state.continuation = scanned.continuation

	if len(state.brackets) == 0 && !state.continuation {
		state.logicalOpen = false
		if state.hasLastValid && state.lastValid == ':' {
			state.expectBlock = true
		}
		return []Event{{Type: EventNewline}}, nil
	}

	state.logicalOpen = true
	return []Event{}, nil
}

func (state *Layout) feedLogicalStart(line []byte) ([]Event, error) {
	if isBlankOrComment(line) {
		state.scannedBytes += len(line)
		return []Event{}, nil
	}

	column, altColumn := measureIndent(line)
	relation := classifyIndent(state.levels, column, altColumn)

	if relation.reason != "" {
		return nil, &Error{Reason: relation.reason}
	}
	if state.expectBlock && relation.kind != relationIndent {
		return nil, &Error{Reason: ReasonExpectedIndent}
	}
	if !state.expectBlock && relation.kind == relationIndent {
		return nil, &Error{ReasonUnexpectedIndent}
	}
	if relation.kind == relationIndent && len(state.levels) >= state.indentLimit+1 {
		return nil, &Error{Reason: ReasonIndentTooDeep}
	}

	word := firstWord(line)
	parent := state.effectiveLevel(relation)
	if reason := danglingReason(word, parent.HeadWord); reason != "" {
		return nil, &Error{Reason: reason}
	}

	scanned := scanLine(line, state.brackets, state.bracketLimit)
	if scanned.reason != "" {
		return nil, &Error{Reason: scanned.reason}
	}

	events := make([]Event, 0, relation.dedentCount+2)
	switch relation.kind {
	case relationIndent:
		state.levels = append(state.levels, Level{
			Column:    column,
			AltColumn: altColumn,
		})
		events = append(events, Event{Type: EventIndent})
		state.indentEvents++
	case relationDedent:
		state.levels = state.levels[:len(state.levels)-relation.dedentCount]
		for range relation.dedentCount {
			events = append(events, Event{Type: EventDedent})
		}
		state.dedentEvents += relation.dedentCount
	}

	state.scannedBytes += len(line)
	state.expectBlock = false
	state.hasLastValid = false
	state.lastValid = 0
	state.brackets = scanned.brackets
	state.continuation = scanned.continuation
	if scanned.hasLastValid && !scanned.continuation {
		state.lastValid = scanned.lastValid
		state.hasLastValid = true
	}
	state.levels[len(state.levels)-1].HeadWord = word

	if len(state.brackets) == 0 && !state.continuation {
		state.logicalOpen = false
		if state.hasLastValid && state.lastValid == ':' {
			state.expectBlock = true
		}
		events = append(events, Event{Type: EventNewline})
		return events, nil
	}

	state.logicalOpen = true
	return events, nil
}

func isBlankOrComment(line []byte) bool {
	for _, ch := range line {
		switch ch {
		case ' ', '\t':
			continue
		case '#':
			return true
		default:
			return false
		}
	}
	return true
}

func measureIndent(line []byte) (int, int) {
	column := 0
	altColumn := 0
	for _, ch := range line {
		if ch == ' ' {
			column++
			altColumn++
		} else if ch == '\t' {
			column = (column + 8) / 8 * 8
			altColumn++
		} else {
			break
		}
	}
	return column, altColumn
}

func classifyIndent(levels []Level, column int, altColumn int) indentRelation {
	top := levels[len(levels)-1]
	if column == top.Column {
		if altColumn != top.AltColumn {
			return indentRelation{kind: relationSame, reason: ReasonInconsistentTab}
		}
		return indentRelation{kind: relationSame}
	}

	if column > top.Column {
		if altColumn <= top.AltColumn {
			return indentRelation{kind: relationIndent, reason: ReasonInconsistentTab}
		}
		return indentRelation{kind: relationIndent}
	}

	candidate := append([]Level(nil), levels...)
	dedentCount := 0
	for len(candidate) > 1 && candidate[len(candidate)-1].Column > column {
		candidate = candidate[:len(candidate)-1]
		dedentCount++
	}
	target := candidate[len(candidate)-1]
	if target.Column != column {
		return indentRelation{kind: relationDedent, dedentCount: dedentCount, reason: ReasonBadDedent}
	}
	if altColumn != target.AltColumn {
		return indentRelation{kind: relationDedent, dedentCount: dedentCount, reason: ReasonInconsistentTab}
	}
	return indentRelation{kind: relationDedent, dedentCount: dedentCount}
}

func (state *Layout) effectiveLevel(relation indentRelation) Level {
	switch relation.kind {
	case relationIndent:
		return Level{}
	case relationDedent:
		return state.levels[len(state.levels)-1-relation.dedentCount]
	default:
		return state.levels[len(state.levels)-1]
	}
}

func firstWord(line []byte) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	start := i
	for i < len(line) && isWordByte(line[i]) {
		i++
	}
	return string(line[start:i])
}

func isWordByte(ch byte) bool {
	return ch == '_' ||
		(ch >= '0' && ch <= '9') ||
		(ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z')
}

func danglingReason(word string, parentWord string) ErrorReason {
	allowed := false
	switch word {
	case "elif":
		allowed = parentWord == "if" || parentWord == "elif"
	case "else":
		allowed = parentWord == "if" || parentWord == "elif" || parentWord == "for" ||
			parentWord == "while" || parentWord == "except"
	case "except":
		allowed = parentWord == "try" || parentWord == "except"
	case "finally":
		allowed = parentWord == "try" || parentWord == "except" || parentWord == "else"
	default:
		return ""
	}
	if allowed {
		return ""
	}
	return ReasonDanglingBranch
}
