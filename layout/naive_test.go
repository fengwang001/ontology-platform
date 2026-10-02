package layout

import (
	"math/rand"
	"testing"
)

type naiveLevel struct {
	column    int
	altColumn int
	headWord  string
}

type naiveMachine struct {
	indentLimit  int
	bracketLimit int
	accepted     []string
	closed       bool
}

type naiveResult struct {
	events []EventType
	reason ErrorReason
	snap   Snapshot
}

type naiveRel struct {
	kind        indentRelationKind
	dedentCount int
	reason      ErrorReason
}

func TestRandomNaiveReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(1168))
	for iteration := 0; iteration < 2000; iteration++ {
		indentLimit := 1 + rng.Intn(3)
		bracketLimit := 1 + rng.Intn(3)
		lineCount := 1 + rng.Intn(14)
		lines := make([]string, lineCount)
		for i := range lines {
			lines[i] = randomLayoutLine(rng)
		}

		actual, _ := New(indentLimit, bracketLimit)
		naive := &naiveMachine{indentLimit: indentLimit, bracketLimit: bracketLimit}
		var actualEvents []EventType
		var naiveEvents []EventType

		for lineIndex, line := range lines {
			actualResponse, actualErr := actual.Feed([]byte(line))
			naiveResponse := naive.feed(line)

			if !sameErrorReason(actualErr, naiveResponse.reason) {
				t.Fatalf("iteration %d line %d input %q\nerror actual=%v naive=%s\nbasis: feed error",
					iteration, lineIndex, lines, actualErr, naiveResponse.reason)
			}
			if actualErr == nil {
				for _, event := range actualResponse {
					actualEvents = append(actualEvents, event.Type)
				}
				naiveEvents = append(naiveEvents, naiveResponse.events...)
			}
			if !snapshotsEqual(actual.Snapshot(), naiveResponse.snap) {
				t.Fatalf("iteration %d line %d input %q\nactual=%+v\nnaive=%+v\nbasis: post-feed state",
					iteration, lineIndex, lines, actual.Snapshot(), naiveResponse.snap)
			}
		}

		actualClose, actualErr := actual.Close()
		naiveClose := naive.close()
		if !sameErrorReason(actualErr, naiveClose.reason) {
			t.Fatalf("iteration %d input %q\nclose actual=%v naive=%s\nbasis: close error",
				iteration, lines, actualErr, naiveClose.reason)
		}
		if actualErr == nil {
			for _, event := range actualClose {
				actualEvents = append(actualEvents, event.Type)
			}
			naiveEvents = append(naiveEvents, naiveClose.events...)
		}
		if !eventsEqual(actualEvents, naiveEvents) {
			t.Fatalf("iteration %d input %q\nactual=%v\nnaive=%v\nbasis: complete event sequence",
				iteration, lines, actualEvents, naiveEvents)
		}
	}
}

func (machine *naiveMachine) feed(line string) naiveResult {
	machine.closed = false
	fresh := &naiveMachine{
		indentLimit:  machine.indentLimit,
		bracketLimit: machine.bracketLimit,
		accepted:     append([]string(nil), machine.accepted...),
	}
	events, reason, snap := fresh.processLine(line)
	if reason == "" {
		machine.accepted = append(machine.accepted, line)
	}
	return naiveResult{events: events, reason: reason, snap: snap}
}

func (machine *naiveMachine) close() naiveResult {
	fresh := &naiveMachine{
		indentLimit:  machine.indentLimit,
		bracketLimit: machine.bracketLimit,
		accepted:     append([]string(nil), machine.accepted...),
	}
	snap := fresh.rebuild()
	if len(snap.BracketStack) > 0 || snap.LogicalOpen && machine.logicalIncomplete() {
		return naiveResult{reason: ReasonIncompleteInput, snap: snap}
	}
	if snap.ExpectBlock {
		return naiveResult{reason: ReasonMissingBlock, snap: snap}
	}
	events := make([]EventType, 0, len(snap.IndentStack))
	for range snap.IndentStack[1:] {
		events = append(events, EventDedent)
	}
	events = append(events, EventEndMarker)
	snap.IndentStack = snap.IndentStack[:1]
	snap.Closed = true
	return naiveResult{events: events, snap: snap}
}

func (machine *naiveMachine) logicalIncomplete() bool {
	snap := machine.rebuild()
	return len(snap.BracketStack) > 0 || snap.LogicalOpen
}

func (machine *naiveMachine) rebuild() Snapshot {
	snap := Snapshot{IndentStack: []Level{{}}}
	var physicalLines []string

	finishIfComplete := func() {
		if len(snap.BracketStack) == 0 && !snap.LogicalOpen {
			physicalLines = nil
		}
	}

	for _, line := range machine.accepted {
		if snap.LogicalOpen {
			physicalLines = append(physicalLines, line)
			snap = naiveScanAll(physicalLines, machine.bracketLimit, snap.IndentStack)
			if len(snap.BracketStack) == 0 && !snap.LogicalOpen {
				physicalLines = nil
			}
			continue
		}

		if naiveBlank(line) {
			continue
		}

		column, altColumn := measureIndent([]byte(line))
		rel := naiveClassify(snap.IndentStack, column, altColumn)
		switch rel.kind {
		case relationIndent:
			snap.IndentStack = append(snap.IndentStack, Level{Column: column, AltColumn: altColumn})
		case relationDedent:
			snap.IndentStack = snap.IndentStack[:len(snap.IndentStack)-rel.dedentCount]
		}
		snap.ExpectBlock = false
		snap.HasLastValidChar = false
		snap.LastValidChar = 0
		snap.BracketStack = nil
		physicalLines = []string{line}
		snap = naiveScanAll(physicalLines, machine.bracketLimit, snap.IndentStack)
		snap.IndentStack[len(snap.IndentStack)-1].HeadWord = firstWord([]byte(line))
		if !snap.LogicalOpen {
			physicalLines = nil
		}
	}
	finishIfComplete()
	return snap
}

func (machine *naiveMachine) processLine(line string) ([]EventType, ErrorReason, Snapshot) {
	if machine.closed {
		return nil, ReasonClosed, Snapshot{}
	}
	if len(line) > 10000 {
		return nil, ReasonLineTooLong, machine.rebuild()
	}

	before := machine.rebuild()
	if before.LogicalOpen {
		return machine.processContinuation(line, before)
	}
	return machine.processStart(line, before)
}

func (machine *naiveMachine) processContinuation(line string, before Snapshot) ([]EventType, ErrorReason, Snapshot) {
	physicalLines := machine.openPhysicalLines()
	physicalLines = append(physicalLines, line)
	if candidateReason := naiveLastScanError(physicalLines, machine.bracketLimit); candidateReason != "" {
		return nil, candidateReason, before
	}
	candidate := naiveScanAll(physicalLines, machine.bracketLimit, append([]Level(nil), before.IndentStack...))
	candidate.HasLastValidChar, candidate.LastValidChar = naiveFinalLastValid(physicalLines)
	if !candidate.LogicalOpen {
		return []EventType{EventNewline}, "", candidate
	}
	return nil, "", candidate
}

func naiveFinalLastValid(lines []string) (bool, byte) {
	var brackets []byte
	lastValid := byte(0)
	hasLast := false
	for _, physicalLine := range lines {
		line := []byte(physicalLine)
		mode := byte(0)
		escaped := false
		pendingBackslash := false
		for i, ch := range line {
			switch mode {
			case '\'', '"':
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' {
					escaped = true
					continue
				}
				if ch == mode {
					mode = 0
					lastValid = ch
					hasLast = true
				}
			case '#':
			default:
				switch ch {
				case '#':
					mode = '#'
				case '\'', '"':
					mode = ch
					lastValid = ch
					hasLast = true
					pendingBackslash = false
				case '(', '[', '{':
					brackets = append(brackets, ch)
					lastValid = ch
					hasLast = true
					pendingBackslash = false
				case ')', ']', '}':
					brackets = brackets[:len(brackets)-1]
					lastValid = ch
					hasLast = true
					pendingBackslash = false
				case ' ', '\t':
				case '\\':
					pendingBackslash = i == len(line)-1
				default:
					lastValid = ch
					hasLast = true
					pendingBackslash = false
				}
			}
		}
		if pendingBackslash {
			if len(brackets) == 0 {
				hasLast = false
				lastValid = 0
			}
		}
	}
	if len(brackets) > 0 {
		return hasLast, lastValid
	}
	return hasLast, lastValid
}

func (machine *naiveMachine) processStart(line string, before Snapshot) ([]EventType, ErrorReason, Snapshot) {
	if naiveBlank(line) {
		return nil, "", before
	}

	column, altColumn := measureIndent([]byte(line))
	rel := naiveClassify(before.IndentStack, column, altColumn)
	if rel.reason != "" {
		return nil, rel.reason, before
	}
	if before.ExpectBlock && rel.kind != relationIndent {
		return nil, ReasonExpectedIndent, before
	}
	if !before.ExpectBlock && rel.kind == relationIndent {
		return nil, ReasonUnexpectedIndent, before
	}
	if rel.kind == relationIndent && len(before.IndentStack) >= machine.indentLimit+1 {
		return nil, ReasonIndentTooDeep, before
	}

	word := firstWord([]byte(line))
	parent := naiveEffectiveLevel(before.IndentStack, rel)
	if reason := danglingReason(word, parent.HeadWord); reason != "" {
		return nil, reason, before
	}

	candidateLevels := append([]Level(nil), before.IndentStack...)
	events := make([]EventType, 0, rel.dedentCount+2)
	switch rel.kind {
	case relationIndent:
		candidateLevels = append(candidateLevels, Level{Column: column, AltColumn: altColumn})
		events = append(events, EventIndent)
	case relationDedent:
		candidateLevels = candidateLevels[:len(candidateLevels)-rel.dedentCount]
		for range rel.dedentCount {
			events = append(events, EventDedent)
		}
	}

	candidate := naiveScanAll([]string{line}, machine.bracketLimit, candidateLevels)
	if reason := naiveLastScanError([]string{line}, machine.bracketLimit); reason != "" {
		return nil, reason, before
	}
	candidate.IndentStack[len(candidate.IndentStack)-1].HeadWord = word
	if !candidate.LogicalOpen {
		events = append(events, EventNewline)
	}
	return events, "", candidate
}

func (machine *naiveMachine) openPhysicalLines() []string {
	var physicalLines []string
	open := false
	for _, line := range machine.accepted {
		if open {
			physicalLines = append(physicalLines, line)
			snap := naiveScanAll(physicalLines, machine.bracketLimit, nil)
			if !snap.LogicalOpen {
				open = false
				physicalLines = nil
			}
			continue
		}
		if naiveBlank(line) {
			continue
		}
		physicalLines = []string{line}
		snap := naiveScanAll(physicalLines, machine.bracketLimit, nil)
		open = snap.LogicalOpen
		if !open {
			physicalLines = nil
		}
	}
	return physicalLines
}

func naiveBlank(line string) bool {
	for _, ch := range []byte(line) {
		if ch == ' ' || ch == '\t' {
			continue
		}
		return ch == '#'
	}
	return true
}

func naiveClassify(levels []Level, column int, altColumn int) naiveRel {
	top := levels[len(levels)-1]
	if column == top.Column {
		if altColumn != top.AltColumn {
			return naiveRel{kind: relationSame, reason: ReasonInconsistentTab}
		}
		return naiveRel{kind: relationSame}
	}
	if column > top.Column {
		if altColumn <= top.AltColumn {
			return naiveRel{kind: relationIndent, reason: ReasonInconsistentTab}
		}
		return naiveRel{kind: relationIndent}
	}

	dedentCount := 0
	candidate := append([]Level(nil), levels...)
	for len(candidate) > 1 && candidate[len(candidate)-1].Column > column {
		candidate = candidate[:len(candidate)-1]
		dedentCount++
	}
	target := candidate[len(candidate)-1]
	if target.Column != column {
		return naiveRel{kind: relationDedent, dedentCount: dedentCount, reason: ReasonBadDedent}
	}
	if altColumn != target.AltColumn {
		return naiveRel{kind: relationDedent, dedentCount: dedentCount, reason: ReasonInconsistentTab}
	}
	return naiveRel{kind: relationDedent, dedentCount: dedentCount}
}

func naiveEffectiveLevel(levels []Level, rel naiveRel) Level {
	switch rel.kind {
	case relationIndent:
		return Level{}
	case relationDedent:
		return levels[len(levels)-1-rel.dedentCount]
	default:
		return levels[len(levels)-1]
	}
}

func naiveScanAll(lines []string, bracketLimit int, levels []Level) Snapshot {
	snap := Snapshot{IndentStack: append([]Level(nil), levels...)}
	for _, physicalLine := range lines {
		line := []byte(physicalLine)
		mode := byte(0)
		escaped := false
		pendingBackslash := false
		for i, ch := range line {
			switch mode {
			case '\'', '"':
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' {
					escaped = true
					continue
				}
				if ch == mode {
					mode = 0
					snap.LastValidChar = ch
					snap.HasLastValidChar = true
				}
			case '#':
			default:
				pendingBackslash = false
				switch ch {
				case '#', '\'', '"':
					if ch == '\'' || ch == '"' {
						mode = ch
						snap.LastValidChar = ch
						snap.HasLastValidChar = true
					} else {
						mode = '#'
					}
				case '(', '[', '{':
					snap.BracketStack = append(snap.BracketStack, ch)
					snap.LastValidChar = ch
					snap.HasLastValidChar = true
				case ')', ']', '}':
					if len(snap.BracketStack) > 0 && bracketsMatch(snap.BracketStack[len(snap.BracketStack)-1], ch) {
						snap.BracketStack = snap.BracketStack[:len(snap.BracketStack)-1]
					}
					snap.LastValidChar = ch
					snap.HasLastValidChar = true
				case ' ', '\t':
				case '\\':
					pendingBackslash = i == len(line)-1
				default:
					snap.LastValidChar = ch
					snap.HasLastValidChar = true
				}
			}
		}
		if mode == '\'' || mode == '"' || escaped {
			snap.LogicalOpen = true
			return snap
		}
		if pendingBackslash {
			snap.LogicalOpen = true
			if len(snap.BracketStack) == 0 {
				snap.HasLastValidChar = false
				snap.LastValidChar = 0
			}
			continue
		}
		if len(snap.BracketStack) > 0 {
			snap.LogicalOpen = true
			continue
		}
		snap.LogicalOpen = false
		if snap.HasLastValidChar && snap.LastValidChar == ':' {
			snap.ExpectBlock = true
		}
	}
	return snap
}

func naiveLastScanError(lines []string, bracketLimit int) ErrorReason {
	var brackets []byte
	for _, physicalLine := range lines {
		line := []byte(physicalLine)
		mode := byte(0)
		escaped := false
		for _, ch := range line {
			switch mode {
			case '\'', '"':
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' {
					escaped = true
					continue
				}
				if ch == mode {
					mode = 0
				}
			case '#':
			default:
				switch ch {
				case '#':
					mode = '#'
				case '\'', '"':
					mode = ch
				case '(', '[', '{':
					brackets = append(brackets, ch)
					if len(brackets) > bracketLimit {
						return ReasonBracketTooDeep
					}
				case ')', ']', '}':
					if len(brackets) == 0 || !bracketsMatch(brackets[len(brackets)-1], ch) {
						return ReasonBracket
					}
					brackets = brackets[:len(brackets)-1]
				}
			}
		}
		if mode == '\'' || mode == '"' || escaped {
			return ReasonString
		}
	}
	return ""
}

func randomLayoutLine(rng *rand.Rand) string {
	switch rng.Intn(8) {
	case 0:
		return randomWhitespace(rng)
	case 1:
		return randomWhitespace(rng) + "# " + randomToken(rng)
	case 2:
		return randomIndent(rng) + randomKeyword(rng) + randomTail(rng) + ":"
	case 3:
		return randomIndent(rng) + randomToken(rng) + " = " + randomTail(rng)
	case 4:
		return randomIndent(rng) + randomBrackets(rng)
	case 5:
		return randomIndent(rng) + randomToken(rng) + " \\"
	case 6:
		return randomIndent(rng) + "x = '" + randomToken(rng)
	default:
		return randomIndent(rng) + randomToken(rng)
	}
}

func randomWhitespace(rng *rand.Rand) string {
	length := rng.Intn(3)
	bytes := make([]byte, length)
	for i := range bytes {
		if rng.Intn(2) == 0 {
			bytes[i] = ' '
		} else {
			bytes[i] = '\t'
		}
	}
	return string(bytes)
}

func randomIndent(rng *rand.Rand) string {
	if rng.Intn(3) == 0 {
		return "\t"
	}
	spaces := rng.Intn(10)
	result := ""
	for range spaces {
		result += " "
	}
	return result
}

func randomKeyword(rng *rand.Rand) string {
	words := []string{"if", "elif", "else", "for", "while", "try", "except", "finally", "elsewhere", "else_", "x"}
	return words[rng.Intn(len(words))]
}

func randomToken(rng *rand.Rand) string {
	words := []string{"x", "y", "foo", "bar", "if", "else", "123", "_q"}
	return words[rng.Intn(len(words))]
}

func randomTail(rng *rand.Rand) string {
	tails := []string{"", " x", " [1]", " ('a')", " # c", " \":\"", " )"}
	return tails[rng.Intn(len(tails))]
}

func randomBrackets(rng *rand.Rand) string {
	pairs := []string{"(", "[", "{", ")", "]", "}", "()", "([])", "([)]", "'('", "# )"}
	return "x = " + pairs[rng.Intn(len(pairs))]
}

func sameErrorReason(err error, reason ErrorReason) bool {
	if err == nil {
		return reason == ""
	}
	layoutErr, ok := err.(*Error)
	return ok && layoutErr.Reason == reason
}

func eventsEqual(left []EventType, right []EventType) bool {
	if len(left) == 0 {
		left = nil
	}
	if len(right) == 0 {
		right = nil
	}
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func snapshotsEqual(left Snapshot, right Snapshot) bool {
	return levelsEqual(left.IndentStack, right.IndentStack) &&
		bytesEqual(left.BracketStack, right.BracketStack) &&
		left.LogicalOpen == right.LogicalOpen &&
		left.LastValidChar == right.LastValidChar &&
		left.HasLastValidChar == right.HasLastValidChar &&
		left.ExpectBlock == right.ExpectBlock &&
		left.Closed == right.Closed
}

func levelsEqual(left []Level, right []Level) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func bytesEqual(left []byte, right []byte) bool {
	if len(left) == 0 {
		left = nil
	}
	if len(right) == 0 {
		right = nil
	}
	return string(left) == string(right)
}
