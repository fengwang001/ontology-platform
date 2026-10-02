package ontology

import "strconv"

const maxTime = int64(1_000_000_000_000_000)

func invalid(field, message string) *RejectError {
	return &RejectError{Kind: RejectInvalidArgument, Field: field, Message: message}
}

func notFound(field string) *RejectError {
	return &RejectError{Kind: RejectNotFound, Field: field}
}

func conflict(field string) *RejectError {
	return &RejectError{Kind: RejectConflict, Field: field}
}

func limitExceeded(field string) *RejectError {
	return &RejectError{Kind: RejectLimitExceeded, Field: field}
}

func validateCertificate(c Certificate) error {
	if len(c.ID) == 0 {
		return invalid("cert.ID", "must not be empty")
	}
	if len(c.Subject) == 0 {
		return invalid("cert.Subject", "must not be empty")
	}
	if len(c.Issuer) == 0 {
		return invalid("cert.Issuer", "must not be empty")
	}
	if len(c.Key) == 0 {
		return invalid("cert.Key", "must not be empty")
	}
	if len(c.AuthKey) == 0 {
		return invalid("cert.AuthKey", "must not be empty")
	}
	if c.NotBefore < 0 || c.NotAfter > maxTime || c.NotBefore >= c.NotAfter {
		return invalid("cert.NotBefore/cert.NotAfter", "invalid validity interval")
	}
	if c.PathLen < -1 || c.PathLen > 100 {
		return invalid("cert.PathLen", "must be -1 or between 0 and 100")
	}
	if !c.IsCA && c.PathLen != -1 {
		return invalid("cert.PathLen", "non-CA certificate must use -1")
	}
	if len(c.Permitted) > 8 {
		return invalid("cert.Permitted", "at most 8 DNS subtrees are allowed")
	}
	if len(c.Excluded) > 8 {
		return invalid("cert.Excluded", "at most 8 DNS subtrees are allowed")
	}
	if len(c.SAN) > 8 {
		return invalid("cert.SAN", "at most 8 SAN entries are allowed")
	}
	if !c.IsCA && (len(c.Permitted) != 0 || len(c.Excluded) != 0) {
		return invalid("cert.Permitted/cert.Excluded", "only CA certificates may carry DNS constraints")
	}
	for idx, subtree := range c.Permitted {
		if !validSubtree(subtree) {
			err := invalid(indexedField("cert.Permitted", idx), "invalid DNS subtree")
			err.Index = idx
			return err
		}
	}
	for idx, subtree := range c.Excluded {
		if !validSubtree(subtree) {
			err := invalid(indexedField("cert.Excluded", idx), "invalid DNS subtree")
			err.Index = idx
			return err
		}
	}
	for idx, san := range c.SAN {
		if !validSAN(san) {
			err := invalid(indexedField("cert.SAN", idx), "invalid DNS SAN")
			err.Index = idx
			return err
		}
	}
	return nil
}

func indexedField(field string, idx int) string {
	return field + "[" + strconv.Itoa(idx) + "]"
}

func validDNSName(value string) bool {
	if value == "" || value[0] == '.' || value[len(value)-1] == '.' {
		return false
	}
	previousDot := false
	for _, char := range value {
		if char == '.' {
			if previousDot {
				return false
			}
			previousDot = true
			continue
		}
		previousDot = false
		if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-') {
			return false
		}
	}
	return true

}

func validSubtree(value string) bool {
	if len(value) > 0 && value[0] == '.' {
		value = value[1:]
	}
	return validDNSName(value)
}

func validSAN(value string) bool {
	if len(value) > 2 && value[0] == '*' && value[1] == '.' {
		return validDNSName(value[2:])
	}
	return validDNSName(value)
}

func validateRequestName(name string) error {
	if !validDNSName(name) {
		return invalid("name", "invalid DNS name")
	}
	return nil
}

func validateTime(at int64, field string) error {
	if at < 0 || at > maxTime {
		return invalid(field, "time must be between 0 and 10^15")
	}
	return nil
}
