package restore

import (
	"encoding/json"
	"io"
)

// writeAuditJSON 将审计轨迹以 JSON Lines 形式写出，供复核与归档。
// 每行一条 AuditEvent，字段与顺序固定；写出过程不触碰快照数据。
func writeAuditJSON(v *Verdict, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, ev := range v.Audit {
		if err := enc.Encode(ev); err != nil {
			return err
		}
	}
	return nil
}
