package resolver

import "strconv"

const maxVersionNumber = 9999

type semanticVersion struct {
	major      int
	minor      int
	patch      int
	prerelease []prereleaseIdentifier
}

type prereleaseIdentifier struct {
	numeric     bool
	numericText string
	text        string
}

func (identifier prereleaseIdentifier) compareTo(other prereleaseIdentifier) int {
	switch {
	case identifier.numeric && other.numeric:
		return compareNumericText(identifier.numericText, other.numericText)
	case identifier.numeric:
		return -1
	case other.numeric:
		return 1
	default:
		return compareBytes(identifier.text, other.text)
	}
}

func parseVersion(text string) (semanticVersion, error) {
	versionCore := text
	var prereleaseText string
	if hyphen := indexByte(text, '-'); hyphen >= 0 {
		versionCore = text[:hyphen]
		prereleaseText = text[hyphen+1:]
	}

	coreParts := splitDot(versionCore)
	if len(coreParts) != 3 {
		return semanticVersion{}, ErrInvalidVersion
	}

	core := make([]int, 3)
	for index, part := range coreParts {
		value, ok := parseBoundedNumber(part)
		if !ok {
			return semanticVersion{}, ErrInvalidVersion
		}
		core[index] = value
	}

	var identifiers []prereleaseIdentifier
	if prereleaseText != "" || len(text) > len(versionCore) {
		parts := splitDot(prereleaseText)
		for _, part := range parts {
			identifier, ok := parsePrereleaseIdentifier(part)
			if !ok {
				return semanticVersion{}, ErrInvalidVersion
			}
			identifiers = append(identifiers, identifier)
		}
	}

	return semanticVersion{
		major:      core[0],
		minor:      core[1],
		patch:      core[2],
		prerelease: identifiers,
	}, nil
}

func compareVersion(left, right semanticVersion) int {
	for _, pair := range [][2]int{
		{left.major, right.major},
		{left.minor, right.minor},
		{left.patch, right.patch},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}

	switch {
	case len(left.prerelease) == 0 && len(right.prerelease) == 0:
		return 0
	case len(left.prerelease) == 0:
		return 1
	case len(right.prerelease) == 0:
		return -1
	}

	for index := 0; index < len(left.prerelease) && index < len(right.prerelease); index++ {
		if result := left.prerelease[index].compareTo(right.prerelease[index]); result != 0 {
			return result
		}
	}

	switch {
	case len(left.prerelease) < len(right.prerelease):
		return -1
	case len(left.prerelease) > len(right.prerelease):
		return 1
	default:
		return 0
	}
}

func parseBoundedNumber(text string) (int, bool) {
	if len(text) == 0 || !allDigits(text) || (len(text) > 1 && text[0] == '0') {
		return 0, false
	}
	value, err := strconv.Atoi(text)
	if err != nil || value < 0 || value > maxVersionNumber {
		return 0, false
	}
	return value, true
}

func parsePrereleaseIdentifier(text string) (prereleaseIdentifier, bool) {
	if len(text) == 0 {
		return prereleaseIdentifier{}, false
	}
	if allDigits(text) {
		if len(text) > 1 && text[0] == '0' {
			return prereleaseIdentifier{}, false
		}
		return prereleaseIdentifier{numeric: true, numericText: text}, true
	}
	if !allAlphaNumericOrHyphen(text) {
		return prereleaseIdentifier{}, false
	}
	return prereleaseIdentifier{text: text}, true
}

func allDigits(text string) bool {
	for index := 0; index < len(text); index++ {
		if text[index] < '0' || text[index] > '9' {
			return false
		}
	}
	return true
}

func allAlphaNumericOrHyphen(text string) bool {
	for index := 0; index < len(text); index++ {
		char := text[index]
		if (char < '0' || char > '9') && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && char != '-' {
			return false
		}
	}
	return true
}

func compareNumericText(left, right string) int {
	trimmedLeft := trimLeadingZeros(left)
	trimmedRight := trimLeadingZeros(right)
	switch {
	case len(trimmedLeft) < len(trimmedRight):
		return -1
	case len(trimmedLeft) > len(trimmedRight):
		return 1
	}
	return compareBytes(trimmedLeft, trimmedRight)
}

func trimLeadingZeros(text string) string {
	for len(text) > 1 && text[0] == '0' {
		text = text[1:]
	}
	return text
}

func compareBytes(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func splitDot(text string) []string {
	parts := []string{}
	start := 0
	for index := 0; index <= len(text); index++ {
		if index == len(text) || text[index] == '.' {
			parts = append(parts, text[start:index])
			start = index + 1
		}
	}
	return parts
}

func indexByte(text string, target byte) int {
	for index := 0; index < len(text); index++ {
		if text[index] == target {
			return index
		}
	}
	return -1
}
