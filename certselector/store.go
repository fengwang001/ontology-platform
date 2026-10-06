package certselector

import "sync/atomic"

// Source identifies where the selected certificate came from.
type Source int

const (
	// SourceExact is an exact SAN match.
	SourceExact Source = iota
	// SourceWildcard is a wildcard SAN match.
	SourceWildcard
	// SourceDefault is a fallback to the default certificate.
	SourceDefault
)

// Selection is the result of a successful selection.
type Selection struct {
	Certificate Certificate
	Source      Source
}

// Selector is a hot-updatable, concurrency-safe certificate set.
type Selector struct {
	idx *index

	// lastExaminedCount records how many candidate certificates the most
	// recent Select inspected; it is used by tests to verify sublinearity.
	lastExaminedCount atomic.Int64
}

// NewSelector creates an empty Selector.
func NewSelector() *Selector {
	return &Selector{idx: newIndex()}
}

// Add inserts a certificate.
func (s *Selector) Add(cert Certificate) error {
	if cert.ID == "" {
		return invalidArg("certificate id must not be empty")
	}
	if cert.Key != ECDSA && cert.Key != RSA {
		return invalidArg("unknown key type")
	}
	if cert.NotBefore < 0 || cert.NotAfter < 0 || cert.NotBefore >= cert.NotAfter {
		return invalidArg("invalid validity interval")
	}
	if len(cert.Names) == 0 {
		return invalidArg("name set must not be empty")
	}

	parsed := make([]parsedName, 0, len(cert.Names))
	seen := make(map[string]struct{}, len(cert.Names))
	for _, san := range cert.Names {
		base, wildcard, err := parseSAN(san)
		if err != nil {
			return err
		}
		key := sanKey(base, wildcard)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		parsed = append(parsed, parsedName{base: base, wildcard: wildcard})
	}

	s.idx.mu.Lock()
	defer s.idx.mu.Unlock()
	if _, exists := s.idx.certs[cert.ID]; exists {
		return ErrConflict
	}

	stored := cert
	stored.Names = append([]string(nil), cert.Names...)
	s.idx.certs[cert.ID] = stored

	for _, pn := range parsed {
		if pn.wildcard {
			if len(s.idx.wild[pn.base]) == 0 {
				s.idx.wildRoot.insert(splitLabels(pn.base))
			}
			s.idx.wild[pn.base] = append(s.idx.wild[pn.base], cert.ID)
		} else {
			s.idx.exact[pn.base] = append(s.idx.exact[pn.base], cert.ID)
		}
	}
	return nil
}

// Remove deletes a certificate by ID and clears a default pointing to it.
func (s *Selector) Remove(id string) error {
	s.idx.mu.Lock()
	defer s.idx.mu.Unlock()

	cert, ok := s.idx.certs[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.idx.certs, id)
	if s.idx.defaults == id {
		s.idx.defaults = ""
	}

	for _, san := range cert.Names {
		base, wildcard, _ := parseSAN(san)
		if wildcard {
			s.idx.wild[base] = removeID(s.idx.wild[base], id)
			if len(s.idx.wild[base]) == 0 {
				delete(s.idx.wild, base)
				s.idx.wildRoot.remove(splitLabels(base))
			}
		} else {
			s.idx.exact[base] = removeID(s.idx.exact[base], id)
			if len(s.idx.exact[base]) == 0 {
				delete(s.idx.exact, base)
			}
		}
	}
	return nil
}

// SetDefault marks an existing certificate as the default.
func (s *Selector) SetDefault(id string) error {
	if id == "" {
		return invalidArg("default id must not be empty")
	}
	s.idx.mu.Lock()
	defer s.idx.mu.Unlock()
	if _, ok := s.idx.certs[id]; !ok {
		return ErrNotFound
	}
	s.idx.defaults = id
	return nil
}

// ClearDefault removes the default certificate assignment.
func (s *Selector) ClearDefault() {
	s.idx.mu.Lock()
	s.idx.defaults = ""
	s.idx.mu.Unlock()
}

// Default returns the current default certificate ID and whether one is set.
func (s *Selector) Default() (string, bool) {
	s.idx.mu.RLock()
	defer s.idx.mu.RUnlock()
	return s.idx.defaults, s.idx.defaults != ""
}

func removeID(ids []string, id string) []string {
	out := ids[:0]
	for _, x := range ids {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}
