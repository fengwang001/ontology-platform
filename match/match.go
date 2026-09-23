package match

import (
	"unicode/utf8"

	"ontology/budget"
	"ontology/pattern"
)

func Match(patternText, targetText string, counter *budget.Budget) (bool, error) {
	parsed, err := pattern.Parse(patternText)
	if err != nil {
		return false, err
	}
	if offset := invalidUTF8(targetText); offset >= 0 {
		return false, pattern.InvalidUTF8Error{Side: pattern.SideText, ByteOffset: offset}
	}

	tokens := parsed.Tokens()
	target := []rune(targetText)
	patternIndex := 0
	targetIndex := 0
	anchorPattern := -1
	anchorTarget := 0

	for patternIndex < len(tokens) {
		if err := counter.Add(1); err != nil {
			return false, err
		}

		if tokens[patternIndex].Kind == pattern.Any {
			patternIndex++
			anchorPattern = patternIndex
			anchorTarget = targetIndex
			continue
		}

		if targetIndex < len(target) && matchesToken(tokens[patternIndex], target[targetIndex]) {
			patternIndex++
			targetIndex++
			continue
		}

		if anchorPattern < 0 {
			return false, nil
		}
		anchorTarget++
		if anchorTarget > len(target) {
			return false, nil
		}
		patternIndex = anchorPattern
		targetIndex = anchorTarget
	}

	return targetIndex == len(target), nil
}

func matchesToken(token pattern.Token, value rune) bool {
	switch token.Kind {
	case pattern.One:
		return true
	case pattern.Literal:
		return token.Rune == value
	default:
		return false
	}
}

func invalidUTF8(value string) int {
	for offset := 0; offset < len(value); {
		r, size := utf8.DecodeRuneInString(value[offset:])
		if r == utf8.RuneError && size == 1 {
			return offset
		}
		offset += size
	}
	return -1
}
