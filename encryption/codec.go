package encryption

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// envelopeVersionV1 是密文信封格式版本。
const envelopeVersionV1 byte = 1

// envelope 是落库的密文结构：只含密文、随机 nonce 与密钥版本，
// 不含任何密钥材料（密钥与数据分离）。
type envelope struct {
	V      byte       `json:"v"`
	KeyVer KeyVersion `json:"k"`
	Nonce  []byte     `json:"n"`
	Cipher []byte     `json:"c"`
}

// Codec 基于 KeyRing 做字段级 AES-256-GCM 加解密。
type Codec struct {
	ring *KeyRing
}

// NewCodec 创建绑定指定密钥环的加解密器。
func NewCodec(ring *KeyRing) *Codec {
	return &Codec{ring: ring}
}

// Seal 用当前密钥与一次性随机 nonce 加密明文。
// 同一明文每次调用产生不同密文，但都可被 Open 解密。
func (c *Codec) Seal(plaintext []byte) ([]byte, error) {
	version, err := c.ring.CurrentVersion()
	if err != nil {
		return nil, err
	}
	key, err := c.ring.get(version)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	// gcm.Seal 在内部对 nonce 唯一性做计数器检查；随机 96 位 nonce
	// 来自 crypto/rand，保证同一密钥下每次加密的 nonce 随机且唯一，
	// 因而相同明文也会产生不同密文。
	sealed := gcm.Seal(nil, nonce, plaintext, nil)
	return marshalEnvelope(envelope{
		V:      envelopeVersionV1,
		KeyVer: version,
		Nonce:  nonce,
		Cipher: sealed,
	})
}

// Open 根据密文信封携带的密钥版本选择对应密钥解密。
func (c *Codec) Open(ciphertext []byte) ([]byte, error) {
	env, err := EnvelopeOf(ciphertext)
	if err != nil {
		return nil, err
	}
	key, err := c.ring.get(env.KeyVer)
	if err != nil {
		// 密钥版本缺失（例如被错误轮换丢弃）会在此明确暴露。
		return nil, fmt.Errorf("%w: version %d", ErrKeyNotFound, env.KeyVer)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(env.Nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("%w: invalid nonce size", ErrBadEnvelope)
	}
	plaintext, err := gcm.Open(nil, env.Nonce, env.Cipher, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadEnvelope, err)
	}
	return plaintext, nil
}

// EnvelopeOf 解析密文信封（用于日志展示 nonce/密钥版本等判定依据）。
func EnvelopeOf(ciphertext []byte) (envelope, error) {
	var e envelope
	if len(ciphertext) < 8 {
		return e, ErrBadEnvelope
	}
	// 二进制信封布局：
	//   magic(4)="ENV1" | version(1) | keyVersion(4,BE,uint32) |
	//   nonceLen(4,BE,uint32) | nonce | gcmCiphertext
	if string(ciphertext[0:4]) != "ENV1" {
		// 兼容 JSON 形式（主要用于调试与测试断言）。
		if err := json.Unmarshal(ciphertext, &e); err == nil && e.V == envelopeVersionV1 {
			return e, nil
		}
		return e, fmt.Errorf("%w: bad magic", ErrBadEnvelope)
	}
	e.V = ciphertext[4]
	if e.V != envelopeVersionV1 {
		return e, fmt.Errorf("%w: unsupported envelope version %d", ErrBadEnvelope, e.V)
	}
	e.KeyVer = KeyVersion(binary.BigEndian.Uint32(ciphertext[5:9]))
	pos := 9
	if len(ciphertext) < pos+4 {
		return e, ErrBadEnvelope
	}
	nonceLen := int(binary.BigEndian.Uint32(ciphertext[pos : pos+4]))
	pos += 4
	if nonceLen <= 0 || len(ciphertext) < pos+nonceLen {
		return e, ErrBadEnvelope
	}
	e.Nonce = append([]byte(nil), ciphertext[pos:pos+nonceLen]...)
	pos += nonceLen
	if pos >= len(ciphertext) {
		return e, ErrBadEnvelope
	}
	e.Cipher = append([]byte(nil), ciphertext[pos:]...)
	return e, nil
}

func marshalEnvelope(e envelope) ([]byte, error) {
	if e.V != envelopeVersionV1 {
		return nil, errors.New("encryption: unsupported envelope version")
	}
	buf := make([]byte, 0, 9+4+len(e.Nonce)+len(e.Cipher))
	buf = append(buf, 'E', 'N', 'V', '1')
	buf = append(buf, e.V)
	var verBytes [4]byte
	binary.BigEndian.PutUint32(verBytes[:], uint32(e.KeyVer))
	buf = append(buf, verBytes[:]...)
	var nonceLen [4]byte
	binary.BigEndian.PutUint32(nonceLen[:], uint32(len(e.Nonce)))
	buf = append(buf, nonceLen[:]...)
	buf = append(buf, e.Nonce...)
	buf = append(buf, e.Cipher...)
	return buf, nil
}
