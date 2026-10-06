package certselector

import (
	"errors"
	"strings"
)

const (
	maxLabelLen = 63
	maxNameLen  = 253
)

// normalizeName lowercases a host name and strips exactly one trailing dot.
// It returns ErrInvalidArgument for empty names, empty labels, whitespace,
// labels longer than 63 bytes, or a total length over 253 bytes.
func normalizeName(name string) (string, error) {
	name = strings.ToLower(name)
	if strings.HasSuffix(name, ".") {
		name = name[:len(name)-1]
	}
	if err := checkLabels(name); err != nil {
		return "", err
	}
	return name, nil
}

func checkLabels(name string) error {
	if name == "" {
		return ErrInvalidArgument
	}
	if len(name) > maxNameLen {
		return ErrInvalidArgument
	}
	if strings.ContainsAny(name, " \t\n\r\v\f") {
		return ErrInvalidArgument
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > maxLabelLen {
			return ErrInvalidArgument
		}
	}
	return nil
}

// parseSAN normalizes and validates one subjectAltName entry.
// A wildcard is allowed only as the whole left-most label "*" and must be
// followed by at least two more labels. The bool result reports wildcards.
func parseSAN(san string) (base string, wildcard bool, err error) {
	if strings.HasPrefix(san, "*.") {
		base = strings.ToLower(san[2:])
		if strings.HasSuffix(base, ".") {
			base = base[:len(base)-1]
		}
		if err := checkLabels(base); err != nil {
			return "", false, err
		}
		if strings.Count(base, ".") < 1 {
			return "", false, ErrInvalidArgument
		}
		if strings.Contains(base, "*") {
			return "", false, ErrInvalidArgument
		}
		return base, true, nil
	}
	if strings.Contains(san, "*") {
		return "", false, ErrInvalidArgument
	}
	base, err = normalizeName(san)
	if err != nil {
		return "", false, err
	}
	return base, false, nil
}

// wildcardMatch reports whether name matches "*.base": name must have exactly
// one label more than base and end with ".base".
func wildcardMatch(name, base string) bool {
	if !strings.HasSuffix(name, "."+base) {
		return false
	}
	left := name[:len(name)-len(base)-1]
	return left != "" && !strings.Contains(left, ".")
}

func invalidArg(msg string) error {
	return errors.Join(ErrInvalidArgument, errors.New(msg))
}
