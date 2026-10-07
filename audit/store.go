package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// RuleVersion 是一个不可变的权限规则版本。
type RuleVersion struct {
	ID          VersionID `json:"id"`
	Seq         uint64    `json:"seq"`
	Content     RuleSet   `json:"content"`
	ContentHash string    `json:"content_hash"`
	PrevChain   string    `json:"prev_chain"`
	ChainHash   string    `json:"chain_hash"`
}

// ReadStats 是单次操作的可观测读取统计，用于证明回放开销
// 仅与目标版本内容规模相关，与系统中版本总数无关。
type ReadStats struct {
	VersionsScanned    int `json:"versions_scanned"`
	VersionBytesRead   int `json:"version_bytes_read"`
	RecordsScanned     int `json:"records_scanned"`
	CorrectionsScanned int `json:"corrections_scanned"`
}

// store 是追加式内存存储：规则版本、审计记录、纠正记录各自成链。
type store struct {
	mu sync.Mutex

	versions     map[VersionID]*RuleVersion
	versionOrder []VersionID
	versionChain string

	records     map[RecordID]*AuditRecord
	recordOrder []RecordID
	recordChain string

	corrections     map[CorrectionID]*Correction
	correctionOrder []CorrectionID
	correctionChain string
	byRecord        map[RecordID][]CorrectionID
}

func newStore() *store {
	return &store{
		versions:    make(map[VersionID]*RuleVersion),
		records:     make(map[RecordID]*AuditRecord),
		corrections: make(map[CorrectionID]*Correction),
		byRecord:    make(map[RecordID][]CorrectionID),
	}
}

func hashParts(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:%s|", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ---- 规则版本 ----

func versionChainHash(prevChain, contentHash string, seq uint64) string {
	return hashParts("rule-version", prevChain, contentHash, fmt.Sprint(seq))
}

func (st *store) appendVersion(content RuleSet) *RuleVersion {
	seq := uint64(len(st.versionOrder)) + 1
	v := &RuleVersion{
		ID:          VersionID(fmt.Sprintf("rv-%d", seq)),
		Seq:         seq,
		Content:     content,
		ContentHash: ContentHash(content),
		PrevChain:   st.versionChain,
	}
	v.ChainHash = versionChainHash(v.PrevChain, v.ContentHash, v.Seq)
	st.versions[v.ID] = v
	st.versionOrder = append(st.versionOrder, v.ID)
	st.versionChain = v.ChainHash
	return v
}

func (st *store) getVersion(id VersionID) (*RuleVersion, bool) {
	v, ok := st.versions[id]
	return v, ok
}

// verifyVersion 以 O(1) 方式校验单个版本条目未被篡改：
// 内容哈希与条目自描述一致，且链哈希与其登记的前向链接一致。
func verifyVersion(v *RuleVersion) bool {
	if ContentHash(v.Content) != v.ContentHash {
		return false
	}
	return versionChainHash(v.PrevChain, v.ContentHash, v.Seq) == v.ChainHash
}

// ---- 审计记录 ----

func recordSelfHash(r *AuditRecord) string {
	payload, err := json.Marshal(struct {
		Seq         uint64    `json:"seq"`
		Subject     string    `json:"subject"`
		Target      string    `json:"target"`
		Request     Request   `json:"request"`
		DecidedAt   time.Time `json:"decided_at"`
		Result      Decision  `json:"result"`
		VersionID   VersionID `json:"version_id"`
		VersionHash string    `json:"version_hash"`
	}{r.Seq, r.Subject, r.Target, r.Request, r.DecidedAt, r.Result, r.VersionID, r.VersionHash})
	if err != nil {
		panic(err)
	}
	return hashParts("audit-record", string(payload))
}

func recordChainHash(prevChain, selfHash string) string {
	return hashParts("audit-chain", prevChain, selfHash)
}

func (st *store) appendRecord(r *AuditRecord) {
	r.Seq = uint64(len(st.recordOrder)) + 1
	r.ID = RecordID(fmt.Sprintf("ar-%d", r.Seq))
	r.PrevChain = st.recordChain
	r.SelfHash = recordSelfHash(r)
	r.ChainHash = recordChainHash(r.PrevChain, r.SelfHash)
	st.records[r.ID] = r
	st.recordOrder = append(st.recordOrder, r.ID)
	st.recordChain = r.ChainHash
}

func (st *store) getRecord(id RecordID) (*AuditRecord, bool) {
	r, ok := st.records[id]
	return r, ok
}

// verifyRecord 以 O(1) 方式校验单条审计记录未被篡改。
func verifyRecord(r *AuditRecord) bool {
	if recordSelfHash(r) != r.SelfHash {
		return false
	}
	return recordChainHash(r.PrevChain, r.SelfHash) == r.ChainHash
}

// ---- 纠正记录 ----

func correctionSelfHash(c *Correction) string {
	payload, err := json.Marshal(struct {
		Seq         uint64       `json:"seq"`
		RecordID    RecordID     `json:"record_id"`
		Supersedes  CorrectionID `json:"supersedes"`
		CorrectedTo Decision     `json:"corrected_to"`
		Reason      string       `json:"reason"`
		RecordedAt  time.Time    `json:"recorded_at"`
	}{c.Seq, c.RecordID, c.Supersedes, c.CorrectedTo, c.Reason, c.RecordedAt})
	if err != nil {
		panic(err)
	}
	return hashParts("correction", string(payload))
}

func correctionChainHash(prevChain, selfHash string) string {
	return hashParts("correction-chain", prevChain, selfHash)
}

func (st *store) appendCorrection(c *Correction) {
	c.Seq = uint64(len(st.correctionOrder)) + 1
	c.ID = CorrectionID(fmt.Sprintf("cr-%d", c.Seq))
	c.PrevChain = st.correctionChain
	c.SelfHash = correctionSelfHash(c)
	c.ChainHash = correctionChainHash(c.PrevChain, c.SelfHash)
	st.corrections[c.ID] = c
	st.correctionOrder = append(st.correctionOrder, c.ID)
	st.correctionChain = c.ChainHash
	st.byRecord[c.RecordID] = append(st.byRecord[c.RecordID], c.ID)
}

func (st *store) getCorrection(id CorrectionID) (*Correction, bool) {
	c, ok := st.corrections[id]
	return c, ok
}

// verifyCorrection 以 O(1) 方式校验单条纠正记录未被篡改。
func verifyCorrection(c *Correction) bool {
	if correctionSelfHash(c) != c.SelfHash {
		return false
	}
	return correctionChainHash(c.PrevChain, c.SelfHash) == c.ChainHash
}
