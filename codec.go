package ontology

import "unicode/utf8"

const (
	// EncodingGSM is selected when every code point belongs to B union X.
	EncodingGSM = "GSM"
	// EncodingUCS2 is selected as soon as one code point is outside B union X.
	EncodingUCS2 = "UCS2"
)

// basicRune reports whether r belongs to the basic character set B.
func basicRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == ' ' || r == '\n':
		return true
	}
	switch r {
	case '.', ',', '!', '?', ':', ';', '-', '_', '@', '#', '%', '&',
		'*', '(', ')', '\'', '"', '+', '=', '/', '<', '>', '$':
		return true
	}
	return false
}

// extendedRune reports whether r belongs to the extension character set X.
func extendedRune(r rune) bool {
	switch r {
	case '{', '}', '[', ']', '~', '^', '|', '\\', '€':
		return true
	}
	return false
}

// splitText validates text, picks its encoding and greedily packs it into
// segments. It runs in time and space proportional to len(text).
func splitText(text string) (*SplitResult, error) {
	if text == "" {
		return nil, newError(ErrInvalidArgument, "Split", "empty text")
	}

	runes := make([]rune, 0, len(text))
	allGSM := true
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size <= 1 {
			return nil, newError(ErrInvalidArgument, "Split", "invalid UTF-8 in text")
		}
		if allGSM && !basicRune(r) && !extendedRune(r) {
			allGSM = false
		}
		runes = append(runes, r)
		i += size
	}

	encoding := EncodingUCS2
	singleLimit, multiCap := 70, 67
	if allGSM {
		encoding = EncodingGSM
		singleLimit, multiCap = 160, 153
	}

	units := make([]int, len(runes))
	total := 0
	for i, r := range runes {
		u := 1
		if allGSM {
			if extendedRune(r) {
				u = 2
			}
		} else if r > 0xFFFF {
			u = 2
		}
		units[i] = u
		total += u
	}

	cap := singleLimit
	if total > singleLimit {
		cap = multiCap
	}

	var segs []Segment
	segStart, used := 0, 0
	for i, u := range units {
		if used+u > cap {
			segs = append(segs, Segment{Start: segStart, End: i})
			segStart = i
			used = u
		} else {
			used += u
		}
	}
	segs = append(segs, Segment{Start: segStart, End: len(runes)})

	if len(segs) > 10 {
		return nil, newError(ErrTooManySegments, "Split", "message requires more than 10 segments")
	}
	return &SplitResult{Encoding: encoding, Segments: segs}, nil
}
