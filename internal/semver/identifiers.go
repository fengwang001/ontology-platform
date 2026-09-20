package semver

import "fmt"

// validateIdentifiers 校验点分标识符序列。
// build=true 时纯数字段允许前导零（build metadata 规则）。
func validateIdentifiers(s string, build bool) error {
	if s == "" {
		return fmt.Errorf("empty identifier list")
	}
	ids := splitDots(s)
	for _, id := range ids {
		if err := validateIdentifier(id, build); err != nil {
			return err
		}
	}
	return nil
}

func validateIdentifier(id string, leadingZeroAllowed bool) error {
	if id == "" {
		return fmt.Errorf("empty identifier")
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' ||
			c >= 'a' && c <= 'z' || c == '-') {
			return fmt.Errorf("illegal character %q in identifier %q", c, id)
		}
	}
	if !leadingZeroAllowed && isAllDigits(id) && len(id) > 1 && id[0] == '0' {
		return fmt.Errorf("numeric identifier %q has leading zero", id)
	}
	return nil
}
