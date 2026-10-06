package certselector

import (
	"strings"
	"unicode"
)

func normalizeClientName(name string) (string, bool) {
	if name == "" {
		return "", true
	}
	return normalizeDomainName(name, false)
}

func normalizeCertificateName(name string, wildcard bool) (string, bool) {
	if name == "" {
		return "", false
	}
	return normalizeDomainName(name, wildcard)
}

func normalizeDomainName(name string, wildcard bool) (string, bool) {
	name = strings.ToLower(name)
	if strings.HasSuffix(name, ".") {
		name = name[:len(name)-1]
	}
	if name == "" || len(name) > 253 {
		return "", false
	}

	if wildcard {
		if !strings.HasPrefix(name, "*.") {
			return "", false
		}
		name = name[2:]
		if name == "" || len(name) > 251 {
			return "", false
		}
	} else if strings.Contains(name, "*") {
		return "", false
	}

	labels := strings.Split(name, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return "", false
		}
		for _, r := range label {
			if unicode.IsSpace(r) {
				return "", false
			}
		}
		if wildcard && strings.ContainsRune(label, '*') {
			return "", false
		}
	}

	if wildcard {
		if len(labels) < 2 {
			return "", false
		}
		return "*." + name, true
	}
	return name, true
}

func wildcardParent(wildcardName string) string {
	return strings.TrimPrefix(wildcardName, "*.")
}

func validateCertificate(certificate Certificate) (Certificate, bool) {
	if certificate.ID == "" {
		return Certificate{}, false
	}
	if certificate.KeyType != KeyTypeEC && certificate.KeyType != KeyTypeRSA {
		return Certificate{}, false
	}
	if certificate.NotAfter < certificate.NotBefore {
		return Certificate{}, false
	}
	if len(certificate.Names) == 0 {
		return Certificate{}, false
	}

	seen := make(map[string]bool, len(certificate.Names))
	normalized := make([]string, 0, len(certificate.Names))
	for _, name := range certificate.Names {
		wildcard := strings.HasPrefix(strings.ToLower(name), "*.")
		normalizedName, ok := normalizeCertificateName(name, wildcard)
		if !ok {
			return Certificate{}, false
		}
		if !seen[normalizedName] {
			seen[normalizedName] = true
			normalized = append(normalized, normalizedName)
		}
	}

	certificate.Names = normalized
	return certificate, true
}
