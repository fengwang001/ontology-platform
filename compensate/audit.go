package compensate

import (
	"encoding/json"
	"fmt"
	"sort"
)

// AuditRecord 是一次幂等判定/消费动作的可事后核查记录。
// 要求：每次判定都记录输入、所依据的事件标识（EventID）、对照结论。
type AuditRecord struct {
	Seq     int64             `json:"seq"`
	Kind    string            `json:"kind"`
	EventID string            `json:"event_id"`
	Detail  map[string]string `json:"detail"`
}

// 审计专用键（与业务命名空间互不相交）。
const (
	auditSeqKey = "comp/audit/seq"
	auditKeyPfx = "comp/audit/rec/"
)

// 审计事件种类。
const (
	AuditHandle        = "HANDLE"         // 一条事件进入消费
	AuditDedup         = "DEDUP_DECISION" // 去重判定：输入标识 + 对照结论
	AuditClaim         = "CLAIM"          // 领取成功（补偿不可撤销点）
	AuditSuperseded    = "SUPERSEDED"     // 撤销先到，放弃补偿
	AuditResume        = "RESUME"         // 崩溃后识别出部分生效边界并续作
	AuditEffect        = "EFFECT_APPLY"   // 单项副作用判定（已生效跳过/新提交）
	AuditComplete      = "COMPLETE"       // 补偿全部生效
	AuditTerminalError = "TERMINAL_ERROR" // 四类终端错误之一（按优先级择一）
)

// AuditLog 是基于 Store 的事务内审计日志。
// 记录与触发它的业务写共用同一个事务：业务写不提交，审计绝不对外可见；
// 业务写提交，审计必然随之可见（不会出现「副作用生效但无判定记录」）。
type AuditLog struct{}

// Append 在 txn 内追加一条记录。Seq 在事务内分配并随事务一起提交，
// 因此已提交 Seq 的全序就是并发事务组的一个合法全局串行顺序证据。
func (AuditLog) Append(txn Txn, kind string, ev Event, detail map[string]string) {
	seq := auditNextSeq(txn)
	rec := AuditRecord{Seq: seq, Kind: kind, EventID: ev.EventID, Detail: detail}
	raw, _ := json.Marshal(rec)
	txn.Put(fmt.Sprintf("%s%020d", auditKeyPfx, seq), raw)
}

func auditNextSeq(txn Txn) int64 {
	var seq int64 = 1
	if raw, err := txn.Get(auditSeqKey); err == nil {
		_ = json.Unmarshal(raw, &seq)
		seq++
	}
	raw, _ := json.Marshal(seq)
	txn.Put(auditSeqKey, raw)
	return seq
}

// Snapshot 返回 MemoryStore 中当前已提交的全部审计记录，按 Seq 升序。
func (AuditLog) Snapshot(s *MemoryStore) []AuditRecord {
	kvs := s.SnapshotPrefix(auditKeyPfx)
	out := make([]AuditRecord, 0, len(kvs))
	for _, raw := range kvs {
		var rec AuditRecord
		if err := json.Unmarshal(raw, &rec); err == nil {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}
