package bcontext

// computeIsolated 在文档装载时刻推导隔离状态。
// 顶层：开启者策略为同源 且 嵌入者策略为要求凭据或无凭据。
// 嵌入框架：顶层当前隔离 且 自身嵌入者策略为要求凭据或无凭据。
// 顶层一旦不隔离，后代一律不隔离；结果在装载时固定，不随后代变化。
func computeIsolated(parent *contextNode, doc Document) bool {
	if parent == nil {
		return doc.Opener == OpenerSameOrigin && doc.Embedder.strict()
	}
	return parent.top.isolated && doc.Embedder.strict()
}

// embedAdmissionOK 判定父文档是否准入子文档嵌入。
// 仅当直接父文档为要求凭据/无凭据嵌入者策略且子为跨源时，
// 才要求子同样声明严格策略；同源子文档不受限制。
func embedAdmissionOK(parent *contextNode, child Document) bool {
	if parent.doc.Embedder.strict() && parent.doc.Origin != child.Origin {
		return child.Embedder.strict()
	}
	return true
}
