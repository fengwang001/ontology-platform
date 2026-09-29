package encryption

import "errors"

var (
	// ErrDeterministicEncryption 表示调用方要求“相同明文产生相同密文”的确定性加密。
	// 本组件只允许随机 nonce 加密，该请求被拒绝且不会改变任何数据。
	ErrDeterministicEncryption = errors.New("encryption: deterministic encryption is forbidden; each encryption must use a random unique nonce")

	// ErrCiphertextOperation 表示调用方试图直接对密文做查询或排序。
	// 敏感字段必须先解密再比较，该请求被拒绝且不会改变任何数据。
	ErrCiphertextOperation = errors.New("encryption: querying or sorting on ciphertext is forbidden; decrypt before comparing")

	// ErrKeyRotationBreaksOldData 表示密钥轮换方案会导致旧密文无法解密
	// （例如删除/替换旧版本密钥）。该请求被拒绝且密钥与数据均不改变。
	ErrKeyRotationBreaksOldData = errors.New("encryption: key rotation must retain old key versions; dropping an old key makes existing ciphertext undecryptable")

	// ErrKeyNotFound 表示密文所携带的密钥版本在 KeyRing 中不存在。
	ErrKeyNotFound = errors.New("encryption: key version not found")

	// ErrObjectNotFound 表示对象不存在。
	ErrObjectNotFound = errors.New("encryption: object not found")

	// ErrBadEnvelope 表示密文信封损坏或版本不支持。
	ErrBadEnvelope = errors.New("encryption: malformed ciphertext envelope")
)
