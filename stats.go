package ontology

// Stats 记录一次连接的可核对统计。两类未匹配严格分开：
// LeftNullKeyRows 只数"键为空"的左行，LeftUnmatchedRows 只数
// "键有值但右表无对应行"的左行，二者互不重叠。
type Stats struct {
	// OutputRows 是本次连接实际产出的结果行数。
	OutputRows int
	// MatchedPairs 是成功匹配的 (左行, 右行) 对数，等于逐键 m*n 之和。
	MatchedPairs int
	// LeftNullKeyRows 是因连接键为空（缺失、nil 或 NaN）而永不匹配的左行数。
	LeftNullKeyRows int
	// LeftUnmatchedRows 是键有值但右表没有对应行的左行数。
	LeftUnmatchedRows int
	// RightUnmatchedRows 是未被任何左行匹配到的右行数。
	RightUnmatchedRows int
	// MaxKeyExpansion 是单键最大展开倍数，即所有匹配键上 m*n 的最大值。
	MaxKeyExpansion int

	expansions map[string]int
}

// KeyExpansions 返回逐键展开明细：键的规范化编码 -> 该键产出的行数 m*n。
// 只包含左右两侧都出现的键；返回的是副本，修改不影响内部状态。
// 全部返回值之和恒等于 MatchedPairs。
func (s *Stats) KeyExpansions() map[string]int {
	out := make(map[string]int, len(s.expansions))
	for k, v := range s.expansions {
		out[k] = v
	}
	return out
}
