package layout

type scanResult struct {
	brackets     []byte
	lastValid    byte
	hasLastValid bool
	continuation bool
	reason       ErrorReason
}

func scanLine(line []byte, brackets []byte, bracketLimit int) scanResult {
	result := scanResult{
		brackets: append([]byte(nil), brackets...),
	}

	const (
		outside = iota
		inSingle
		inDouble
		inComment
	)

	state := outside
	escaped := false
	pendingBackslash := false

	for i, ch := range line {
		switch state {
		case inSingle, inDouble:
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			closing := byte('\'')
			if state == inDouble {
				closing = '"'
			}
			if ch == closing {
				state = outside
				result.lastValid = ch
				result.hasLastValid = true
			}

		case inComment:
			continue

		default:
			switch ch {
			case '#':
				state = inComment
			case '\'', '"':
				if ch == '\'' {
					state = inSingle
				} else {
					state = inDouble
				}
				pendingBackslash = false
				result.lastValid = ch
				result.hasLastValid = true
			case '(', '[', '{':
				pendingBackslash = false
				if len(result.brackets) >= bracketLimit {
					result.reason = ReasonBracketTooDeep
					return result
				}
				result.brackets = append(result.brackets, ch)
				result.lastValid = ch
				result.hasLastValid = true
			case ')', ']', '}':
				if len(result.brackets) == 0 || !bracketsMatch(result.brackets[len(result.brackets)-1], ch) {
					result.reason = ReasonBracket
					return result
				}
				result.brackets = result.brackets[:len(result.brackets)-1]
				pendingBackslash = false
				result.lastValid = ch
				result.hasLastValid = true
			case ' ', '\t':
			case '\\':
				pendingBackslash = i == len(line)-1
			default:
				pendingBackslash = false
				result.lastValid = ch
				result.hasLastValid = true
			}
		}
	}

	if state == inSingle || state == inDouble || escaped {
		result.reason = ReasonString
		return result
	}

	result.continuation = pendingBackslash
	return result
}

func bracketsMatch(open byte, close byte) bool {
	switch open {
	case '(':
		return close == ')'
	case '[':
		return close == ']'
	case '{':
		return close == '}'
	default:
		return false
	}
}
