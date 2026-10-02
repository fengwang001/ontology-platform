package ontology

const (
	minTime         = 0
	maxTime         = 1_000_000_000_000_000
	maxLinks        = 8
	maxPathLenField = 100
)

func validDNSName(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '.') {
			return false
		}
	}
	if s[len(s)-1] == '.' {
		return false
	}
	prevDot := false
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			if prevDot {
				return false
			}
			prevDot = true
		} else {
			prevDot = false
		}
	}
	return true
}

// validSubtree 校验 DNS 子树约束串：允许以单个点开头。
func validSubtree(s string) bool {
	if len(s) == 0 {
		return false
	}
	if s[0] == '.' {
		if len(s) == 1 {
			return false
		}
		return validDNSName(s[1:]) && s[1] != '.'
	}
	return validDNSName(s)
}

// validSAN 校验 SAN 串：允许以「*.」开头（通配占最左一个完整标签）。
func validSAN(s string) bool {
	if len(s) >= 2 && s[0] == '*' && s[1] == '.' {
		base := s[2:]
		if len(base) == 0 || base[0] == '.' {
			return false
		}
		return validDNSName(base)
	}
	return validDNSName(s)
}

func validateCert(c Cert) error {
	if len(c.ID) == 0 {
		return errConfig("Add", "ID must be non-empty")
	}
	if len(c.Subject) == 0 {
		return errConfig("Add", "Subject must be non-empty")
	}
	if len(c.Issuer) == 0 {
		return errConfig("Add", "Issuer must be non-empty")
	}
	if len(c.Key) == 0 {
		return errConfig("Add", "Key must be non-empty")
	}
	if len(c.AuthKey) == 0 {
		return errConfig("Add", "AuthKey must be non-empty")
	}
	if c.NotBefore < minTime || c.NotAfter > maxTime || c.NotBefore >= c.NotAfter {
		return errConfig("Add", "require 0 <= NotBefore < NotAfter <= 1e15")
	}
	if c.PathLen != -1 && (c.PathLen < 0 || c.PathLen > maxPathLenField) {
		return errConfig("Add", "PathLen must be -1 or in [0,100]")
	}
	if !c.IsCA && c.PathLen != -1 {
		return errConfig("Add", "non-CA cert must have PathLen -1")
	}
	if len(c.Permitted) > maxLinks {
		return errConfig("Add", "too many Permitted entries")
	}
	if len(c.Excluded) > maxLinks {
		return errConfig("Add", "too many Excluded entries")
	}
	if len(c.SAN) > maxLinks {
		return errConfig("Add", "too many SAN entries")
	}
	if !c.IsCA && (len(c.Permitted) > 0 || len(c.Excluded) > 0) {
		return errConfig("Add", "only CA certs may carry subtree constraints")
	}
	for _, s := range c.Permitted {
		if !validSubtree(s) {
			return errConfig("Add", "invalid Permitted subtree: "+s)
		}
	}
	for _, s := range c.Excluded {
		if !validSubtree(s) {
			return errConfig("Add", "invalid Excluded subtree: "+s)
		}
	}
	for _, s := range c.SAN {
		if !validSAN(s) {
			return errConfig("Add", "invalid SAN entry: "+s)
		}
	}
	return nil
}
