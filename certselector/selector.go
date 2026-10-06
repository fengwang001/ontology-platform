package certselector

import (
	"strings"
	"sync"
)

type Selector struct {
	mu           sync.RWMutex
	certificates map[string]*certificateRecord
	index        nameIndex
	defaultID    string
}

func New() *Selector {
	return &Selector{
		certificates: make(map[string]*certificateRecord),
		index: nameIndex{
			exact:     make(map[string]*scheduleGroup),
			wildcards: make(map[string]*scheduleGroup),
		},
	}
}

func (s *Selector) Add(certificate Certificate) error {
	normalized, ok := validateCertificate(certificate)
	if !ok {
		return ErrInvalidArgument
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.certificates[normalized.ID]; exists {
		return ErrCertificateConflict
	}

	record := &certificateRecord{certificate: normalized}
	changedExact := make([]string, 0, len(normalized.Names))
	changedWildcards := make([]string, 0, len(normalized.Names))
	for _, name := range normalized.Names {
		if name[0] == '*' {
			parent := wildcardParent(name)
			record.wildcards = append(record.wildcards, parent)
			s.index.wildcards[parent] = appendRecord(s.index.wildcards[parent], record)
			changedWildcards = append(changedWildcards, parent)
		} else {
			record.exactNames = append(record.exactNames, name)
			s.index.exact[name] = appendRecord(s.index.exact[name], record)
			changedExact = append(changedExact, name)
		}
	}
	s.certificates[normalized.ID] = record
	for _, name := range changedExact {
		rebuildGroup(s.index.exact, name)
	}
	for _, parent := range changedWildcards {
		rebuildGroup(s.index.wildcards, parent)
	}
	return nil
}

func (s *Selector) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, exists := s.certificates[id]
	if !exists {
		return ErrCertificateNotFound
	}

	for _, name := range record.exactNames {
		s.index.exact[name] = removeRecord(s.index.exact[name], record)
		rebuildGroup(s.index.exact, name)
	}
	for _, parent := range record.wildcards {
		s.index.wildcards[parent] = removeRecord(s.index.wildcards[parent], record)
		rebuildGroup(s.index.wildcards, parent)
	}
	delete(s.certificates, id)
	if s.defaultID == id {
		s.defaultID = ""
	}
	return nil
}

func (s *Selector) SetDefault(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.certificates[id]; !exists {
		return ErrCertificateNotFound
	}
	s.defaultID = id
	return nil
}

func (s *Selector) RemoveDefault() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultID = ""
}

func (s *Selector) Select(input SelectInput) (Selection, error) {
	name, ok := normalizeClientName(input.Name)
	supportedCount := 0
	for _, supported := range input.KeyTypes {
		if supported {
			supportedCount++
		}
	}
	if !ok || input.Now < 0 || supportedCount == 0 {
		return Selection{}, ErrInvalidArgument
	}
	for keyType := range input.KeyTypes {
		if keyType != KeyTypeEC && keyType != KeyTypeRSA {
			return Selection{}, ErrInvalidArgument
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if name != "" {
		if group := s.index.exact[name]; group != nil {
			return selectFromGroup(group, MatchExact, input.KeyTypes, input.Now)
		}
		if _, parent, ok := strings.Cut(name, "."); ok {
			if group := s.index.wildcards[parent]; group != nil {
				return selectFromGroup(group, MatchWildcard, input.KeyTypes, input.Now)
			}
		}
	}

	if s.defaultID == "" {
		return Selection{}, ErrNoMatchingCertificate
	}
	record := s.certificates[s.defaultID]
	certificate := record.certificate
	if input.Now < certificate.NotBefore || input.Now >= certificate.NotAfter {
		return Selection{}, ErrNoValidCertificate
	}
	if !input.KeyTypes[certificate.KeyType] {
		return Selection{}, ErrUnsupportedKeyType
	}
	return Selection{Certificate: cloneCertificate(certificate), Source: MatchDefault}, nil
}

func appendRecord(group *scheduleGroup, record *certificateRecord) *scheduleGroup {
	if group == nil {
		return &scheduleGroup{certificates: []*certificateRecord{record}}
	}
	group.certificates = append(group.certificates, record)
	return group
}

func removeRecord(group *scheduleGroup, record *certificateRecord) *scheduleGroup {
	if group == nil {
		return nil
	}
	certificates := make([]*certificateRecord, 0, len(group.certificates))
	for _, candidate := range group.certificates {
		if candidate != record {
			certificates = append(certificates, candidate)
		}
	}
	group.certificates = certificates
	return group
}

func selectFromGroup(group *scheduleGroup, source MatchSource, supported map[KeyType]bool, now int64) (Selection, error) {
	if supported[KeyTypeEC] {
		if record := group.bestAt(now, KeyTypeEC); record != nil {
			return Selection{Certificate: cloneCertificate(record.certificate), Source: source}, nil
		}
	}
	if supported[KeyTypeRSA] {
		if record := group.bestAt(now, KeyTypeRSA); record != nil {
			return Selection{Certificate: cloneCertificate(record.certificate), Source: source}, nil
		}
	}

	if group.bestAt(now, KeyTypeEC) != nil || group.bestAt(now, KeyTypeRSA) != nil {
		return Selection{}, ErrUnsupportedKeyType
	}
	return Selection{}, ErrNoValidCertificate
}

func cloneCertificate(certificate Certificate) Certificate {
	certificate.Names = append([]string(nil), certificate.Names...)
	return certificate
}
