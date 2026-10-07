// Package naive is an independently maintained, deliberately simple reference
// implementation of the same audit/replay semantics as package audit.
//
// It uses plain slices, linear scans and a single big lock. It intentionally
// shares no code with package audit (not even its types) beyond trivial
// primitives: the differential test re-encodes both worlds' outputs through
// string values. Any divergence between the two implementations is a defect in
// one of them.
package naive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
)

var (
	ErrAuditNotFound           = errors.New("audit record not found")
	ErrVersionNotFound         = errors.New("rule version not found")
	ErrVersionTampered         = errors.New("rule version integrity violated")
	ErrAuditTampered           = errors.New("audit record integrity violated")
	ErrCorrectionTargetMissing = errors.New("correction target record not found")
	ErrCorrectionTampered      = errors.New("correction record integrity violated")
	ErrCorrectionConflict      = errors.New("correction does not advance the chain conclusion")
	ErrReplayMatch             = errors.New("replay matches original decision; no correction needed")
	ErrInvalidInput            = errors.New("invalid input")
	ErrAlreadyExists           = errors.New("record already exists")
)

type Rule struct {
	ID       string
	Subjects []string
	Targets  []string
	Actions  []string
	Effect   string
	Order    int
}

type Version struct {
	ID          string
	Seq         int64
	ParentID    string
	Rules       []Rule
	ContentHash string
}

type Audit struct {
	ID            string
	Seq           int64
	Subject       string
	Target        string
	Action        string
	Content       string
	Allow         bool
	RuleVersionID string
	EngineTag     string
	RecordHash    string
}

type Correction struct {
	ID             string
	Seq            int64
	AuditID        string
	TargetType     string
	TargetID       string
	CorrectedAllow bool
	RecordHash     string
}

// Reference is the naive reference system.
type Reference struct {
	versions []Version
	audits   []Audit
	corr     []Correction
	// engineForAudit records which answer producer to use per audit seq: the
	// differential harness registers deliberately defective engines.
	answers map[int64]bool
}

// New returns an empty reference.
func New() *Reference {
	return &Reference{answers: map[int64]bool{}}
}

func hsum(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type auditBody struct {
	ID            string `json:"id"`
	Seq           int64  `json:"seq"`
	Subject       string `json:"subject"`
	Target        string `json:"target"`
	Action        string `json:"action"`
	Content       string `json:"content"`
	Allow         bool   `json:"allow"`
	RuleVersionID string `json:"rule_version_id"`
	EngineTag     string `json:"engine_tag"`
}

type correctionBody struct {
	ID             string `json:"id"`
	Seq            int64  `json:"seq"`
	AuditID        string `json:"audit_id"`
	TargetType     string `json:"target_type"`
	TargetID       string `json:"target_id"`
	CorrectedAllow bool   `json:"corrected_allow"`
}

func auditHash(a Audit) string {
	return hsum(auditBody{a.ID, a.Seq, a.Subject, a.Target, a.Action, a.Content, a.Allow, a.RuleVersionID, a.EngineTag})
}

func correctionHash(c Correction) string {
	return hsum(correctionBody{c.ID, c.Seq, c.AuditID, c.TargetType, c.TargetID, c.CorrectedAllow})
}

// CommitVersion appends a version.
func (r *Reference) CommitVersion(id, parentID string, rules []Rule) error {
	if id == "" {
		return ErrInvalidInput
	}
	for _, v := range r.versions {
		if v.ID == id {
			return ErrAlreadyExists
		}
	}
	if len(r.versions) == 0 {
		if parentID != "" {
			return ErrVersionNotFound
		}
	} else if r.versions[len(r.versions)-1].ID != parentID {
		return ErrInvalidInput
	}
	for _, rule := range rules {
		if rule.ID == "" || (rule.Effect != "ALLOW" && rule.Effect != "DENY") {
			return ErrInvalidInput
		}
	}
	v := Version{ID: id, Seq: int64(len(r.versions)) + 1, ParentID: parentID, Rules: rules}
	v.ContentHash = hsum(struct {
		ID       string `json:"id"`
		ParentID string `json:"parent_id"`
		Rules    []Rule `json:"rules"`
	}{id, parentID, rules})
	r.versions = append(r.versions, v)
	return nil
}

func (r *Reference) findVersion(id string) (Version, int) {
	for i, v := range r.versions {
		if v.ID == id {
			return v, i
		}
	}
	return Version{}, -1
}

// canonical decision: sort by (order,id), first match, default deny.
func decide(rules []Rule, subj, tgt, act string) bool {
	ordered := append([]Rule(nil), rules...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Order != ordered[j].Order {
			return ordered[i].Order < ordered[j].Order
		}
		return ordered[i].ID < ordered[j].ID
	})
	for _, rule := range ordered {
		if contains(rule.Subjects, subj) && contains(rule.Targets, tgt) && contains(rule.Actions, act) {
			return rule.Effect == "ALLOW"
		}
	}
	return false
}

func contains(set []string, v string) bool {
	if len(set) == 0 {
		return true
	}
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// RecordAudit appends an audit record. forcedAnswer != nil simulates a
// defective historical engine; tag labels it.
func (r *Reference) RecordAudit(subj, tgt, act, content, versionID string, forcedAnswer *bool, tag string) (Audit, error) {
	if subj == "" || tgt == "" || act == "" || versionID == "" {
		return Audit{}, ErrInvalidInput
	}
	v, _ := r.findVersion(versionID)
	if v.ID == "" {
		return Audit{}, ErrVersionNotFound
	}
	allow := decide(v.Rules, subj, tgt, act)
	engineTag := "canonical:v1"
	if forcedAnswer != nil {
		allow = *forcedAnswer
		if tag != "" {
			engineTag = tag
		} else {
			engineTag = "forced:v1"
		}
	}
	seq := int64(len(r.audits)) + 1
	a := Audit{
		ID: "audit-" + jsonNum(seq), Seq: seq, Subject: subj, Target: tgt, Action: act,
		Content: content, Allow: allow, RuleVersionID: versionID, EngineTag: engineTag,
	}
	a.RecordHash = auditHash(a)
	r.audits = append(r.audits, a)
	return a, nil
}

func jsonNum(n int64) string {
	return strconv.FormatInt(n, 10)
}

func (r *Reference) findAudit(id string) (Audit, int) {
	for i, a := range r.audits {
		if a.ID == id {
			return a, i
		}
	}
	return Audit{}, -1
}

// ReplayResult mirrors audit.ReplayReport as plain values.
type ReplayResult struct {
	AuditID       string
	VersionID     string
	OriginalAllow bool
	ReplayedAllow bool
	Outcome       string
	Err           error
}

// Replay rebuilds one decision from its named version.
func (r *Reference) Replay(auditID string) ReplayResult {
	a, _ := r.findAudit(auditID)
	if a.ID == "" {
		return ReplayResult{Err: ErrAuditNotFound}
	}
	res := ReplayResult{AuditID: a.ID, VersionID: a.RuleVersionID, OriginalAllow: a.Allow}
	v, _ := r.findVersion(a.RuleVersionID)
	if v.ID == "" {
		res.Err = ErrVersionNotFound
		return res
	}
	expected := hsum(struct {
		ID       string `json:"id"`
		ParentID string `json:"parent_id"`
		Rules    []Rule `json:"rules"`
	}{v.ID, v.ParentID, v.Rules})
	if expected != v.ContentHash {
		res.Outcome = "INTEGRITY_VERSION_TAMPERED"
		res.Err = ErrVersionTampered
		return res
	}
	if auditHash(a) != a.RecordHash {
		res.Outcome = "INTEGRITY_AUDIT_TAMPERED"
		res.Err = ErrAuditTampered
		return res
	}
	res.ReplayedAllow = decide(v.Rules, a.Subject, a.Target, a.Action)
	if res.ReplayedAllow == a.Allow {
		res.Outcome = "MATCH"
	} else {
		res.Outcome = "MISMATCH_DEFECTIVE_ORIGINAL"
	}
	return res
}

// AppendCorrection appends an independent correction.
func (r *Reference) AppendCorrection(id, auditID string, correctedAllow bool, reason string) (Correction, ReplayResult, error) {
	a, _ := r.findAudit(auditID)
	if a.ID == "" {
		return Correction{}, ReplayResult{}, ErrAuditNotFound
	}
	for _, c := range r.corr {
		if c.ID == id {
			return Correction{}, ReplayResult{}, ErrAlreadyExists
		}
	}
	res := r.Replay(auditID)
	if res.Err != nil {
		return Correction{}, res, res.Err
	}
	var chain []Correction
	for _, c := range r.corr {
		if c.AuditID == auditID {
			if correctionHash(c) != c.RecordHash {
				return Correction{}, res, ErrCorrectionTampered
			}
			chain = append(chain, c)
		}
	}
	var targetType, targetID string
	var headConclusion bool
	if len(chain) == 0 {
		targetType, targetID, headConclusion = "AUDIT", a.ID, a.Allow
	} else {
		h := chain[len(chain)-1]
		targetType, targetID, headConclusion = "CORRECTION", h.ID, h.CorrectedAllow
	}
	if correctedAllow == headConclusion {
		return Correction{}, res, ErrCorrectionConflict
	}
	if len(chain) == 0 && res.Outcome == "MATCH" {
		return Correction{}, res, ErrReplayMatch
	}
	if id == "" {
		return Correction{}, res, ErrInvalidInput
	}
	c := Correction{
		ID: id, Seq: int64(len(r.corr)) + 1, AuditID: auditID,
		TargetType: targetType, TargetID: targetID, CorrectedAllow: correctedAllow,
	}
	c.RecordHash = correctionHash(c)
	r.corr = append(r.corr, c)
	return c, res, nil
}

// LegalityResult mirrors audit.LegalityView.
type LegalityResult struct {
	AuditID        string
	Kind           string
	OriginalAllow  bool
	EffectiveAllow bool
	ActiveID       string
	Err            error
}

// Legality returns the three-way view.
func (r *Reference) Legality(auditID string) LegalityResult {
	a, _ := r.findAudit(auditID)
	if a.ID == "" {
		return LegalityResult{Err: ErrAuditNotFound}
	}
	if auditHash(a) != a.RecordHash {
		return LegalityResult{AuditID: auditID, Err: ErrAuditTampered}
	}
	var chain []Correction
	for _, c := range r.corr {
		if c.AuditID == auditID {
			if correctionHash(c) != c.RecordHash {
				return LegalityResult{AuditID: auditID, Err: ErrCorrectionTampered}
			}
			chain = append(chain, c)
		}
	}
	res := LegalityResult{AuditID: auditID, OriginalAllow: a.Allow, EffectiveAllow: a.Allow}
	if len(chain) > 0 {
		res.ActiveID = chain[len(chain)-1].ID
		res.EffectiveAllow = chain[len(chain)-1].CorrectedAllow
	}
	if res.EffectiveAllow != a.Allow {
		res.Kind = "CORRECTED"
	} else if a.Allow {
		res.Kind = "ORIGINAL_ALLOWED"
	} else {
		res.Kind = "ORIGINAL_DENIED"
	}
	return res
}

// VersionCount / AuditCount / CorrectionCount are test helpers.
func (r *Reference) VersionCount() int    { return len(r.versions) }
func (r *Reference) AuditCount() int      { return len(r.audits) }
func (r *Reference) CorrectionCount() int { return len(r.corr) }
