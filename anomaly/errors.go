package anomaly

// 拒绝原因，按题面给定的检查顺序定义。
const (
	RejectZeroID         = "zero-or-duplicate-txn-id"
	RejectBadReadVersion = "read-references-unknown-version"
	RejectBadOrder       = "malformed-version-order"
	RejectTooManyTxns    = "too-many-transactions"
)
