package hypcheck

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
)

// AuditEntry 记录一次判定过程：输入、依据的钩子版本 / 权限快照、结论。
// 记录以 prevHash 形成哈希链，任何中途篡改都可被 Verify 重新计算发现。
type AuditEntry struct {
	AuditID   string          `json:"audit_id"`
	Seq       Seq             `json:"seq"`
	LinearSeq Seq             `json:"linear_seq"`
	Request   PrecheckRequest `json:"request"`
	Result    PrecheckResult  `json:"result"`
	PermTrace *PermTrace      `json:"perm_trace,omitempty"`
	PrevHash  string          `json:"prev_hash"`
	EntryHash string          `json:"entry_hash"`
}

// Auditor 是审计接收器（内存实现见 MemoryAuditor）。
type Auditor interface {
	Record(entry AuditEntry) AuditEntry
	Entries() []AuditEntry
	Verify() error
}

// MemoryAuditor 是线程安全的追加式哈希链审计器。
type MemoryAuditor struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func NewMemoryAuditor() *MemoryAuditor { return &MemoryAuditor{} }

// AuditHash 计算单条记录除 EntryHash 外内容的 SHA-256（含 prevHash）。
func AuditHash(e AuditEntry) string {
	cp := e
	cp.EntryHash = ""
	b, _ := json.Marshal(cp)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (a *MemoryAuditor) Record(entry AuditEntry) AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry.Seq = Seq(len(a.entries) + 1)
	entry.AuditID = "a" + strconv.FormatInt(int64(entry.Seq), 10)
	if len(a.entries) > 0 {
		entry.PrevHash = a.entries[len(a.entries)-1].EntryHash
	} else {
		entry.PrevHash = ""
	}
	entry.EntryHash = AuditHash(entry)
	a.entries = append(a.entries, entry)
	return entry
}

func (a *MemoryAuditor) Entries() []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AuditEntry, len(a.entries))
	copy(out, a.entries)
	return out
}

// Verify 重算整条哈希链，发现断链或内容被改即返回错误。
func (a *MemoryAuditor) Verify() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	prev := ""
	for i, e := range a.entries {
		if e.PrevHash != prev {
			return fmt.Errorf("audit: entry %d prev hash mismatch", i+1)
		}
		if got := AuditHash(e); got != e.EntryHash {
			return fmt.Errorf("audit: entry %d content hash mismatch", i+1)
		}
		prev = e.EntryHash
	}
	return nil
}

func makeAuditEntry(req PrecheckRequest, res *PrecheckResult, linear Seq, stats *ProbeStats) AuditEntry {
	return AuditEntry{
		LinearSeq: linear,
		Request:   req,
		Result:    *res,
		PermTrace: res.PermTrace,
	}
}
