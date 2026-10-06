package certselector

// failureReason classifies why a selection failed.
type failureReason int

const (
	reasonNoMatch failureReason = iota
	reasonExpired
	reasonUnsupportedKey
)

// FailureError carries the classified failure reason.
type FailureError struct {
	reason failureReason
	source Source
}

func (e *FailureError) Error() string {
	switch e.reason {
	case reasonExpired:
		return ErrExpired.Error()
	case reasonUnsupportedKey:
		return ErrUnsupportedKey.Error()
	default:
		return ErrNoMatch.Error()
	}
}

func (e *FailureError) Unwrap() error {
	switch e.reason {
	case reasonExpired:
		return ErrExpired
	case reasonUnsupportedKey:
		return ErrUnsupportedKey
	default:
		return ErrNoMatch
	}
}

// Select chooses a certificate for the given client name, supported key
// types and current time.
func (s *Selector) Select(name string, supported map[KeyType]bool, now int64) (Selection, error) {
	if len(supported) == 0 {
		return Selection{}, invalidArg("supported key type set must not be empty")
	}
	for key := range supported {
		if key != ECDSA && key != RSA {
			return Selection{}, invalidArg("unknown key type in supported set")
		}
	}
	if now < 0 {
		return Selection{}, invalidArg("now must be non-negative")
	}

	var norm string
	hadName := name != ""
	if hadName {
		var err error
		norm, err = normalizeName(name)
		if err != nil {
			return Selection{}, err
		}
	}

	idx := s.idx
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var ids []string
	source := SourceDefault
	if hadName {
		if exactIDs := idx.exact[norm]; len(exactIDs) > 0 {
			ids = append(ids, exactIDs...)
			source = SourceExact
		} else if base, ok := idx.wildRoot.wildcardBase(splitLabels(norm)); ok {
			ids = append(ids, idx.wild[base]...)
			source = SourceWildcard
		}
	}

	if len(ids) == 0 {
		if idx.defaults == "" {
			return Selection{}, &FailureError{reason: reasonNoMatch, source: SourceDefault}
		}
		ids = []string{idx.defaults}
		source = SourceDefault
	}

	examined := 0
	timeValidCount := 0
	var best *Certificate
	for _, id := range ids {
		cert, ok := idx.certs[id]
		if !ok {
			continue
		}
		examined++
		if now < cert.NotBefore || now >= cert.NotAfter {
			continue
		}
		timeValidCount++
		if supported[cert.Key] && (best == nil || prefer(cert, *best)) {
			c := cert
			best = &c
		}
	}
	s.lastExaminedCount.Store(int64(examined))

	if best != nil {
		return Selection{Certificate: *best, Source: source}, nil
	}
	reason := reasonExpired
	if timeValidCount > 0 {
		reason = reasonUnsupportedKey
	}
	return Selection{}, &FailureError{reason: reason, source: source}
}

// prefer reports whether a should be chosen over b.
// Precedence: ECDSA over RSA, later NotAfter, then smaller ID.
func prefer(a, b Certificate) bool {
	if a.Key != b.Key {
		return a.Key == ECDSA
	}
	if a.NotAfter != b.NotAfter {
		return a.NotAfter > b.NotAfter
	}
	return a.ID < b.ID
}
