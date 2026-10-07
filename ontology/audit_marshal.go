package ontology

import "strconv"

// marshalForDigest 产生确定性的、无歧义的字段序列化，用于审计指纹。
// 不依赖 encoding/json 的 map 顺序：所有字段按固定顺序拼接，
// 字符串用长度前缀定界，保证不可注入。
func marshalForDigest(r *AuditRecord) []byte {
	b := make([]byte, 0, 256)
	appendStr := func(s string) {
		b = strconv.AppendInt(b, int64(len(s)), 10)
		b = append(b, ':')
		b = append(b, s...)
	}
	appendStr(r.RebuildID)
	appendStr(string(r.Type))
	appendStr(string(r.Attr))
	b = strconv.AppendInt(b, r.BaselineSeq, 10)
	b = append(b, ';')
	b = strconv.AppendInt(b, r.CompleteSeq, 10)
	b = append(b, ';')
	for _, e := range r.Entries {
		appendStr(string(e.Object))
		appendStr(string(e.Value))
		b = strconv.AppendInt(b, e.SourceSeq, 10)
		b = append(b, ',')
		if e.Baseline {
			b = append(b, '1')
		} else {
			b = append(b, '0')
		}
		b = append(b, ';')
	}
	return b
}
