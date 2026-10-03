package ontology

import (
	"strconv"
	"strings"
)

func validateConfig(config Config) error {
	if config.AuthPendingTTL < 1 || config.AuthPendingTTL > 1_000_000_000 ||
		config.AuthValidTTL < 1 || config.AuthValidTTL > 1_000_000_000 ||
		config.OrderTTL < 1 || config.OrderTTL > 1_000_000_000 ||
		config.FailureWindow < 1 || config.FailureWindow > 1_000_000_000 ||
		config.FailureThreshold < 1 || config.FailureThreshold > 1_000 ||
		config.NonceCapacity < 1 || config.NonceCapacity > 1_000_000 ||
		config.PendingAuthLimit < 1 || config.PendingAuthLimit > 10_000 {
		return errInvalid()
	}
	return nil
}

func validNow(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000_000
}

func validateAccount(account []byte) (string, error) {
	if len(account) == 0 {
		return "", errInvalid()
	}
	return string(account), nil
}

func validIdentifierList(identifiers []string) bool {
	if len(identifiers) < 1 || len(identifiers) > 10 {
		return false
	}
	seen := make(map[string]struct{}, len(identifiers))
	for _, identifier := range identifiers {
		if !validIdentifier(identifier) {
			return false
		}
		if _, ok := seen[identifier]; ok {
			return false
		}
		seen[identifier] = struct{}{}
	}
	return true
}

func validIdentifier(identifier string) bool {
	name := identifier
	if strings.HasPrefix(name, "*.") {
		name = name[2:]
	}
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func parseAuthID(value string) (int, error) {
	return parseNumberedID(value, "z")
}

func parseOrderID(value string) (int, error) {
	return parseNumberedID(value, "o")
}

func parseNumberedID(value, prefix string) (int, error) {
	if value == "" || !strings.HasPrefix(value, prefix) {
		return 0, errInvalid()
	}
	number := value[len(prefix):]
	if number == "" {
		return 0, errInvalid()
	}
	for i := 0; i < len(number); i++ {
		if number[i] < '0' || number[i] > '9' {
			return 0, errInvalid()
		}
	}
	id, err := strconv.Atoi(number)
	if err != nil || id < 1 {
		return 0, errInvalid()
	}
	return id, nil
}

func sameStringSet(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	seen := make(map[string]int, len(want))
	for _, value := range want {
		seen[value]++
	}
	for _, value := range got {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
	}
	return true
}

func errInvalid() error {
	return categorized(KindInvalidArgument, "", ErrInvalidArgument)
}

func conflict(current string) error {
	return categorized(KindConflict, current, ErrConflict)
}

func rateLimited(identifier string, count int) error {
	return &Error{
		Kind:       KindRateLimited,
		Current:    strconv.Itoa(count),
		Identifier: identifier,
		Count:      count,
		err:        ErrRateLimited,
	}
}

func quotaExceeded(p, q int) error {
	return &Error{Kind: KindQuotaExceeded, Current: strconv.Itoa(p + q), P: p, Q: q, err: ErrQuotaExceeded}
}

func categorized(kind, current string, base error) error {
	return &Error{Kind: kind, Current: current, err: base}
}
