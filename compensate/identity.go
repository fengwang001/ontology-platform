package compensate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Fingerprint 返回事件内容（不含 EventID 本身）的规范化指纹。
// 用途：当同一个 EventID 的重复投递携带了与首次投递不一致的内容时，
// 系统无法判断它究竟是同一次执行的损坏投递，还是两个不同事件被错误复用了 ID——
// 这正是 E3_AMBIGUOUS_IDENTITY。绝不允许「按内容猜一个」继续补偿。
func Fingerprint(ev Event) string {
	keys := make([]string, 0, len(ev.Payload))
	for k := range ev.Payload {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	canonical := struct {
		ActionType string            `json:"action_type"`
		Payload    map[string]string `json:"payload"`
	}{ActionType: ev.ActionType}
	canonical.Payload = map[string]string{}
	for _, k := range keys {
		canonical.Payload[k] = ev.Payload[k]
	}
	raw, _ := json.Marshal(canonical)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ValidIdentity 判断事件自身的标识信息是否足以判定它与其他事件的关系。
// 唯一硬性要求：EventID 非空。EventID 由生产者在动作提交事务内分配，
// 这是区分「网络重试的重复投递」与「独立的等价二次调用」的唯一依据。
func ValidIdentity(ev Event) bool { return ev.EventID != "" }

func encodeRecord(rec *Record) []byte {
	raw, _ := json.Marshal(rec)
	return raw
}

func decodeRecord(raw []byte) (*Record, error) {
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}
