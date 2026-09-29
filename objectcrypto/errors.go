package objectcrypto

import "errors"

var (
	ErrDeterministicCiphertext = errors.New("objectcrypto: deterministic ciphertext is rejected (random nonce required)")
	ErrCiphertextQuery         = errors.New("objectcrypto: querying or sorting ciphertext directly is rejected (decrypt first)")
	ErrKeyRotatedUnreadable    = errors.New("objectcrypto: old data unreadable after key rotation is rejected (retired key version missing)")
	ErrUnknownKeyVersion       = errors.New("objectcrypto: ciphertext references an unknown key version")
	ErrNonceReuse              = errors.New("objectcrypto: nonce reuse detected for the same key")
)
