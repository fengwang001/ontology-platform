package resolver

import "strings"

type comparator struct {
	operator string
	version  semanticVersion
}

type versionRange struct {
	comparators []comparator
}

func parseRange(text string) (versionRange, error) {
	terms := strings.Fields(text)
	if len(terms) == 0 {
		return versionRange{}, ErrInvalidRange
	}

	comparators := make([]comparator, 0, len(terms))
	for _, term := range terms {
		operator, versionText := splitOperator(term)
		if operator == "" {
			return versionRange{}, ErrInvalidRange
		}
		version, err := parseVersion(versionText)
		if err != nil {
			return versionRange{}, ErrInvalidRange
		}
		comparators = append(comparators, comparator{operator: operator, version: version})
	}

	return versionRange{comparators: comparators}, nil
}

func (r versionRange) allows(candidate semanticVersion) bool {
	if len(candidate.prerelease) > 0 && !r.hasPrereleaseGate(candidate) {
		return false
	}
	for _, comparator := range r.comparators {
		if !comparator.allows(candidate) {
			return false
		}
	}
	return true
}

func (r versionRange) hasPrereleaseGate(candidate semanticVersion) bool {
	for _, comparator := range r.comparators {
		if len(comparator.version.prerelease) > 0 && sameCore(candidate, comparator.version) {
			return true
		}
	}
	return false
}

func (comparator comparator) allows(candidate semanticVersion) bool {
	switch comparator.operator {
	case "=":
		return compareVersion(candidate, comparator.version) == 0
	case ">":
		return compareVersion(candidate, comparator.version) > 0
	case ">=":
		return compareVersion(candidate, comparator.version) >= 0
	case "<":
		return compareVersion(candidate, comparator.version) < 0
	case "<=":
		return compareVersion(candidate, comparator.version) <= 0
	case "^":
		return compareVersion(candidate, comparator.version) >= 0 &&
			compareVersion(candidate, caretUpperBound(comparator.version)) < 0
	case "~":
		return compareVersion(candidate, comparator.version) >= 0 &&
			compareVersion(candidate, tildeUpperBound(comparator.version)) < 0
	default:
		return false
	}
}

func splitOperator(term string) (string, string) {
	for _, operator := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(term, operator) && len(term) > len(operator) {
			return operator, term[len(operator):]
		}
	}
	return "", term
}

func sameCore(left, right semanticVersion) bool {
	return left.major == right.major && left.minor == right.minor && left.patch == right.patch
}

func caretUpperBound(version semanticVersion) semanticVersion {
	switch {
	case version.major > 0:
		return semanticVersion{major: version.major + 1}
	case version.minor > 0:
		return semanticVersion{minor: version.minor + 1}
	default:
		return semanticVersion{patch: version.patch + 1}
	}
}

func tildeUpperBound(version semanticVersion) semanticVersion {
	return semanticVersion{major: version.major, minor: version.minor + 1}
}
