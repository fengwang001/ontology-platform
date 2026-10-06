package certselector

import (
	"sort"
	"strings"
	"unicode"
)

type naiveModel struct {
	certs     map[string]Certificate
	defaultID string
}

type naiveResult struct {
	id     string
	source MatchSource
	err    error
}

func newNaiveModel() *naiveModel {
	return &naiveModel{certs: make(map[string]Certificate)}
}

func naiveNormalize(name string, wildcard bool) (string, bool) {
	name = strings.ToLower(name)
	name = strings.TrimSuffix(name, ".")
	if name == "" || len(name) > 253 {
		return "", false
	}
	if wildcard {
		if !strings.HasPrefix(name, "*.") {
			return "", false
		}
		name = name[2:]
		if strings.Contains(name, "*") || len(name) == 0 {
			return "", false
		}
	} else if strings.Contains(name, "*") {
		return "", false
	}

	labels := strings.Split(name, ".")
	if wildcard && len(labels) < 2 {
		return "", false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return "", false
		}
		for _, r := range label {
			if unicode.IsSpace(r) {
				return "", false
			}
		}
	}
	if wildcard {
		return "*." + name, true
	}
	return name, true
}

func (m *naiveModel) add(input Certificate) (string, error) {
	if input.ID == "" || (input.KeyType != KeyTypeEC && input.KeyType != KeyTypeRSA) || input.NotAfter < input.NotBefore || len(input.Names) == 0 {
		return "invalid", ErrInvalidArgument
	}

	seen := map[string]bool{}
	names := make([]string, 0, len(input.Names))
	for _, name := range input.Names {
		normalized, ok := naiveNormalize(name, strings.HasPrefix(strings.ToLower(name), "*."))
		if !ok {
			return "invalid", ErrInvalidArgument
		}
		if !seen[normalized] {
			seen[normalized] = true
			names = append(names, normalized)
		}
	}

	if _, exists := m.certs[input.ID]; exists {
		return "conflict", ErrCertificateConflict
	}
	input.Names = names
	m.certs[input.ID] = input
	return "added", nil
}

func (m *naiveModel) remove(id string) (string, error) {
	if _, exists := m.certs[id]; !exists {
		return "not-found", ErrCertificateNotFound
	}
	delete(m.certs, id)
	if m.defaultID == id {
		m.defaultID = ""
	}
	return "removed", nil
}

func (m *naiveModel) setDefault(id string) (string, error) {
	if _, exists := m.certs[id]; !exists {
		return "not-found", ErrCertificateNotFound
	}
	m.defaultID = id
	return "default-set", nil
}

func (m *naiveModel) clearDefault() string {
	m.defaultID = ""
	return "default-cleared"
}

func naiveWildcardMatches(candidateName string, clientName string) bool {
	if !strings.HasPrefix(candidateName, "*.") {
		return false
	}
	first, parent, ok := strings.Cut(clientName, ".")
	return ok && first != "" && parent == candidateName[2:]
}

func (m *naiveModel) selectCertificate(input SelectInput) (naiveResult, string) {
	name := ""
	ok := true
	if input.Name != "" {
		name, ok = naiveNormalize(input.Name, false)
	}
	supported := map[KeyType]bool{}
	for keyType, value := range input.KeyTypes {
		if keyType != KeyTypeEC && keyType != KeyTypeRSA {
			ok = false
		}
		if value {
			supported[keyType] = true
		}
	}
	if !ok || input.Now < 0 || len(supported) == 0 {
		return naiveResult{err: ErrInvalidArgument}, "invalid selection input"
	}

	all := make([]Certificate, 0, len(m.certs))
	for _, certificate := range m.certs {
		all = append(all, certificate)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })

	var matches []Certificate
	source := MatchDefault
	if name != "" {
		for _, certificate := range all {
			for _, candidateName := range certificate.Names {
				if candidateName == name {
					matches = append(matches, certificate)
					break
				}
			}
		}
		if len(matches) > 0 {
			source = MatchExact
		} else {
			for _, certificate := range all {
				for _, candidateName := range certificate.Names {
					if naiveWildcardMatches(candidateName, name) {
						matches = append(matches, certificate)
						break
					}
				}
			}
			if len(matches) > 0 {
				source = MatchWildcard
			}
		}
	}

	if len(matches) == 0 {
		if m.defaultID == "" {
			return naiveResult{err: ErrNoMatchingCertificate}, "no name match and no default"
		}
		certificate := m.certs[m.defaultID]
		if input.Now < certificate.NotBefore || input.Now >= certificate.NotAfter {
			return naiveResult{err: ErrNoValidCertificate}, "default certificate outside validity interval"
		}
		if !supported[certificate.KeyType] {
			return naiveResult{err: ErrUnsupportedKeyType}, "default certificate key type unsupported"
		}
		return naiveResult{id: certificate.ID, source: MatchDefault}, "default fallback selected"
	}

	activeSupported := make([]Certificate, 0, len(matches))
	activeCount := 0
	for _, certificate := range matches {
		if input.Now >= certificate.NotBefore && input.Now < certificate.NotAfter {
			activeCount++
			if supported[certificate.KeyType] {
				activeSupported = append(activeSupported, certificate)
			}
		}
	}
	if len(activeSupported) == 0 {
		if activeCount > 0 {
			return naiveResult{err: ErrUnsupportedKeyType}, "matched active certificate but key types unsupported"
		}
		return naiveResult{err: ErrNoValidCertificate}, "matched certificates all outside validity interval"
	}

	sort.Slice(activeSupported, func(i, j int) bool {
		left := activeSupported[i]
		right := activeSupported[j]
		if left.KeyType != right.KeyType {
			return left.KeyType == KeyTypeEC
		}
		if left.NotAfter != right.NotAfter {
			return left.NotAfter > right.NotAfter
		}
		return left.ID < right.ID
	})

	reason := "matched candidates ordered by key type, expiry, then id"
	if source == MatchExact {
		reason = "exact " + reason
	} else {
		reason = "wildcard " + reason
	}
	return naiveResult{id: activeSupported[0].ID, source: source}, reason
}
