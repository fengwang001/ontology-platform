package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// canonicalRule is the fixed field order used when hashing a rule.
type canonicalRule struct {
	ID       string   `json:"id"`
	Order    int      `json:"order"`
	Effect   string   `json:"effect"`
	Subjects []string `json:"subjects"`
	Targets  []string `json:"targets"`
	Actions  []string `json:"actions"`
}

func canonicalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

func digest(parts ...any) (string, error) {
	h := sha256.New()
	enc := json.NewEncoder(h)
	enc.SetEscapeHTML(false)
	for _, p := range parts {
		b, err := canonicalJSON(p)
		if err != nil {
			return "", err
		}
		h.Write(b)
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// canonicaliseRules returns a deterministic copy of the rule set: rules sorted
// by (Order, ID) and every member slice sorted lexicographically. Wildcards
// (empty member lists) are preserved.
func canonicaliseRules(rs RuleSet) ([]canonicalRule, error) {
	rules := make([]Rule, len(rs.Rules))
	copy(rules, rs.Rules)
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Order != rules[j].Order {
			return rules[i].Order < rules[j].Order
		}
		return rules[i].ID < rules[j].ID
	})
	out := make([]canonicalRule, 0, len(rules))
	for _, r := range rules {
		cr := canonicalRule{
			ID:       r.ID,
			Order:    r.Order,
			Effect:   string(r.Effect),
			Subjects: sortedCopy(r.Subjects),
			Targets:  sortedCopy(r.Targets),
			Actions:  sortedCopy(r.Actions),
		}
		out = append(out, cr)
	}
	return out, nil
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

type versionContent struct {
	ID       string          `json:"id"`
	ParentID string          `json:"parent_id"`
	Rules    []canonicalRule `json:"rules"`
}

// hashVersionContent binds exactly the rule version content that must never
// change: version id, parent id and canonicalised rules.
func hashVersionContent(in RuleVersionInput) (string, []byte, error) {
	rules, err := canonicaliseRules(in.Rules)
	if err != nil {
		return "", nil, err
	}
	vc := versionContent{ID: in.ID, ParentID: in.ParentID, Rules: rules}
	b, err := canonicalJSON(vc)
	if err != nil {
		return "", nil, err
	}
	h, err := digest(string(b))
	if err != nil {
		return "", nil, err
	}
	return h, b, nil
}

type versionChain struct {
	Seq           int64  `json:"seq"`
	ID            string `json:"id"`
	ParentID      string `json:"parent_id"`
	ContentHash   string `json:"content_hash"`
	PrevChainHash string `json:"prev_chain_hash"`
}

func hashVersionChain(v RuleVersion) (string, error) {
	return digest(versionChain{
		Seq:           v.Seq,
		ID:            v.ID,
		ParentID:      v.ParentID,
		ContentHash:   v.ContentHash,
		PrevChainHash: v.PrevChainHash,
	})
}

type auditBody struct {
	ID            string `json:"id"`
	Seq           int64  `json:"seq"`
	Subject       string `json:"subject"`
	Target        string `json:"target"`
	Action        string `json:"action"`
	Content       string `json:"content"`
	DecidedAt     string `json:"decided_at"`
	Allow         bool   `json:"allow"`
	RuleVersionID string `json:"rule_version_id"`
	EngineTag     string `json:"engine_tag"`
	PrevHash      string `json:"prev_hash"`
}

func hashAudit(a AuditRecord) (string, error) {
	return digest(auditBody{
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
	})
}

type correctionBody struct {
	ID             string `json:"id"`
	Seq            int64  `json:"seq"`
	AuditID        string `json:"audit_id"`
	TargetType     string `json:"target_type"`
	TargetID       string `json:"target_id"`
	CorrectedAllow bool   `json:"corrected_allow"`
	Reason         string `json:"reason"`
	CreatedAt      string `json:"created_at"`
	PrevHash       string `json:"prev_hash"`
}

func hashCorrection(c Correction) (string, error) {
	return digest(correctionBody{
		ID:             c.ID,
		Seq:            c.Seq,
		AuditID:        c.AuditID,
		TargetType:     string(c.TargetType),
		TargetID:       c.TargetID,
		CorrectedAllow: c.CorrectedAllow,
		Reason:         c.Reason,
		CreatedAt:      c.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999"),
		PrevHash:       c.PrevHash,
	})
}
