package audit

import (
	"encoding/json"
	"time"
)

// Store is the in-memory, append-only persistence layer.
//
// It exposes metered point lookups so callers can observe how much material a
// replay actually read. Replay performs exactly one version lookup and thus
// its version read volume depends only on that one version, never on the
// number of versions accumulated.
type Store struct {
	versionsByID map[string]RuleVersion
	versionOrder []string
	auditsByID   map[string]AuditRecord
	auditOrder   []string
	corrections  []Correction
	corrByID     map[string]Correction
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{
		versionsByID: map[string]RuleVersion{},
		auditsByID:   map[string]AuditRecord{},
		corrByID:     map[string]Correction{},
	}
}

// VersionCount returns the number of committed rule versions.
func (s *Store) VersionCount() int { return len(s.versionOrder) }

// AuditCount returns the number of committed audit records.
func (s *Store) AuditCount() int { return len(s.auditOrder) }

// CorrectionCount returns the number of committed correction records.
func (s *Store) CorrectionCount() int { return len(s.corrections) }

// appendVersion commits one new version. The caller is responsible for hash
// computation and chain linkage.
func (s *Store) appendVersion(v RuleVersion) {
	s.versionsByID[v.ID] = v
	s.versionOrder = append(s.versionOrder, v.ID)
}

// getVersion returns the stored version and the size in bytes of the version
// material read. Replay uses this single-point lookup only: the metered size
// is the canonical encoding of exactly this one version's rule content plus
// its fixed-size metadata, and does not depend on any other version.
func (s *Store) getVersion(id string) (RuleVersion, int64, bool) {
	v, ok := s.versionsByID[id]
	if !ok {
		return RuleVersion{}, 0, false
	}
	return v, measureVersionBytes(v), true
}

func measureVersionBytes(v RuleVersion) int64 {
	rules, _ := canonicaliseRules(v.Rules)
	payload := versionContent{ID: v.ID, ParentID: v.ParentID, Rules: rules}
	b, err := json.Marshal(payload)
	if err != nil {
		return 0
	}
	return int64(len(b))
}

// headVersion returns the most recently committed version, or nil.
func (s *Store) headVersion() *RuleVersion {
	if len(s.versionOrder) == 0 {
		return nil
	}
	v := s.versionsByID[s.versionOrder[len(s.versionOrder)-1]]
	return &v
}

// appendAudit commits one new audit record.
func (s *Store) appendAudit(a AuditRecord) {
	s.auditsByID[a.ID] = a
	s.auditOrder = append(s.auditOrder, a.ID)
}

// getAudit returns the stored audit record and its encoded byte size.
func (s *Store) getAudit(id string) (AuditRecord, int64, bool) {
	a, ok := s.auditsByID[id]
	if !ok {
		return AuditRecord{}, 0, false
	}
	return a, measureAuditBytes(a), true
}

func measureAuditBytes(a AuditRecord) int64 {
	body := auditBody{
		ID:            a.ID,
		Seq:           a.Seq,
		Subject:       a.Subject,
		Target:        a.Target,
		Action:        a.Action,
		Content:       a.Content,
		DecidedAt:     a.DecidedAt.UTC().Format("2006-01-02T15:04:05.999999999"),
		Allow:         a.Allow,
		RuleVersionID: a.RuleVersionID,
		EngineTag:     a.EngineTag,
		PrevHash:      a.PrevHash,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return 0
	}
	return int64(len(b))
}

// listAudits returns all audit records in commit order.
func (s *Store) listAudits() []AuditRecord {
	out := make([]AuditRecord, 0, len(s.auditOrder))
	for _, id := range s.auditOrder {
		out = append(out, s.auditsByID[id])
	}
	return out
}

// appendCorrection commits one new correction record.
func (s *Store) appendCorrection(c Correction) {
	s.corrByID[c.ID] = c
	s.corrections = append(s.corrections, c)
}

// getCorrection returns one correction by ID.
func (s *Store) getCorrection(id string) (Correction, bool) {
	c, ok := s.corrByID[id]
	return c, ok
}

// listCorrections returns all correction records in commit order.
func (s *Store) listCorrections() []Correction {
	out := make([]Correction, len(s.corrections))
	copy(out, s.corrections)
	return out
}

// now is overridable in tests.
var now = func() time.Time { return time.Now().UTC() }
