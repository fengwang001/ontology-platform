package ontology

// SigStatus 是注入式签名校验器对单个签名给出的三种回答。
type SigStatus int

const (
	SigValid SigStatus = iota
	SigInvalid
	SigUnknownKey
)

// SignatureValidator 由外部注入；identity 为提交或标签的标识。
type SignatureValidator interface {
	Verify(identity string, sig Signature) SigStatus
}
