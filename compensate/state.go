package compensate

// 补偿记录状态机（持久化在存储中，键名见 key 常量）：
//
//	(无记录)
//	   │ Handle（与撤销请求竞争，原子 CAS）
//	   ├── Undo 先到 ──▶ SUPERSEDED（终态：放弃补偿）
//	   └── Handle 先到 ─▶ CLAIMED（领券完成；此后不可撤销、不可放弃）
//	                        │ 逐项提交副作用
//	                        ├── 崩溃重启 → 续作（只补未生效项）
//	                        ├── E1/E2/E4 ─▶ FAILED（冻结边界，不扩大副作用）
//	                        └── 全部生效 ─▶ COMPLETED（终态）
//
// 重复投递（相同 EventID）到达任意状态：不产生任何新副作用，直接按既有状态返回。
type RecordState string

const (
	StateClaimed    RecordState = "CLAIMED"
	StateCompleted  RecordState = "COMPLETED"
	StateFailed     RecordState = "FAILED"
	StateSuperseded RecordState = "SUPERSEDED"
)

// 存储键命名空间（互不相交）。
const (
	// keyRecord 前缀：补偿记录（状态机 + 已生效副作用位图 + 事件指纹）。
	keyRecord = "comp/rec/"
	// keyUndo 前缀：撤销请求标记（UndoArrived 先于领取则存在）。
	keyUndo = "comp/undo/"
)

func recordKey(eventID string) string { return keyRecord + eventID }
func undoKey(eventID string) string   { return keyUndo + eventID }

// Record 是每条事件（每次动作执行）的持久化补偿记录。
// 它与去重索引是同一份事实：记录存在即代表此 EventID 已被处理过。
// 因此判定「是否重复」只需要对 recordKey(EventID) 做一次定点 Get，
// 探测次数恒为 1，与历史事件总量无关（测试 TestDedupLookupConstantTime 独立验证）。
type Record struct {
	State        RecordState `json:"state"`
	EventID      string      `json:"event_id"`
	ActionType   string      `json:"action_type"`
	Fingerprint  string      `json:"fingerprint"` // 首次投递负载的规范化指纹
	EffectCount  int         `json:"effect_count"`
	Applied      []bool      `json:"applied"` // 每项副作用是否已确认生效
	FailedClass  ErrorClass  `json:"failed_class,omitempty"`
	FailedReason string      `json:"failed_reason,omitempty"`
	ClaimSeq     int64       `json:"claim_seq"`
}
