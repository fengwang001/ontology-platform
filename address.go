package ontology

func normalizeAddress(address string) (normalized, keyDomain, originalDomain string, ok bool) {
	if len(address) == 0 || len(address) > 254 {
		return "", "", "", false
	}

	bytes := make([]byte, len(address))
	at := -1
	for i := 0; i < len(address); i++ {
		c := address[i]
		if c <= 0x20 || c == 0x7f {
			return "", "", "", false
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		bytes[i] = c
		if c == '@' {
			if at != -1 {
				return "", "", "", false
			}
			at = i
		}
	}

	if at <= 0 || at == len(address)-1 {
		return "", "", "", false
	}

	local := string(bytes[:at])
	domain := string(bytes[at+1:])
	if !validDomain(domain) {
		return "", "", "", false
	}

	keyDomain = domain
	originalDomain = domain
	if plus := indexByte(local, '+'); plus >= 0 {
		local = local[:plus]
	}
	if local == "" {
		return "", "", "", false
	}

	if domain == "googlemail.com" || domain == "gmail.com" {
		withoutDots := make([]byte, 0, len(local))
		for i := 0; i < len(local); i++ {
			if local[i] != '.' {
				withoutDots = append(withoutDots, local[i])
			}
		}
		local = string(withoutDots)
		if local == "" {
			return "", "", "", false
		}
	}

	if domain == "googlemail.com" {
		keyDomain = "gmail.com"
	}
	return local + "@" + keyDomain, keyDomain, originalDomain, true
}

func validDomain(domain string) bool {
	if len(domain) == 0 || domain[0] == '.' || domain[len(domain)-1] == '.' {
		return false
	}
	for i := 0; i < len(domain); i++ {
		if domain[i] == '.' && (i == 0 || domain[i-1] == '.') {
			return false
		}
	}
	return indexByte(domain, '.') >= 0
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}
