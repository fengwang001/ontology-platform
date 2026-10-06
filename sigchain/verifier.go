package sigchain

// CryptoResult 为注入校验器对签名真伪的回答。
type CryptoResult int

const (
	CryptoValid      CryptoResult = iota // 有效
	CryptoInvalid                        // 无效
	CryptoKeyUnknown                     // 密钥未知
)

func (c CryptoResult) String() string {
	switch c {
	case CryptoValid:
		return "有效"
	case CryptoInvalid:
		return "无效"
	case CryptoKeyUnknown:
		return "密钥未知"
	}
	return "未知结果"
}

// Verifier 由外部注入，回答某对象上某签名的密码学真伪。
// 实现必须线程安全，且不得在回调中再调用 Service 的方法。
type Verifier interface {
	Verify(objectID string, sig Signature) CryptoResult
}
