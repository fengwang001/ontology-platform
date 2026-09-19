package ontology

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
)

// 游标格式（全部经 RawURLEncoding Base64 编码后对外，不暴露主键明文）：
//
//	version(1) | sessionID(16) | position(8, big endian) | hmacSHA256(32)
//
// HMAC 以 Store 的进程内随机密钥对前面的明文字段做完整性保护。
// 伪造、截断或篡改任一字节都会在校验阶段失败并返回 ErrCursorMalformed。

const (
	cursorVersion = 1
	cursorIDLen   = 16
	cursorPosLen  = 8
	cursorSigLen  = sha256.Size
	cursorBodyLen = 1 + cursorIDLen + cursorPosLen
	cursorTotal   = cursorBodyLen + cursorSigLen
)

func encodeCursor(secret, sid []byte, position int64) string {
	body := make([]byte, cursorTotal)
	body[0] = cursorVersion
	copy(body[1:1+cursorIDLen], sid)
	binary.BigEndian.PutUint64(body[1+cursorIDLen:cursorBodyLen], uint64(position))
	mac := hmac.New(sha256.New, secret)
	mac.Write(body[:cursorBodyLen])
	copy(body[cursorBodyLen:], mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString(body)
}

func decodeCursor(secret []byte, encoded string) (sid []byte, position int64, err error) {
	raw, decErr := base64.RawURLEncoding.DecodeString(encoded)
	if decErr != nil || len(raw) != cursorTotal || raw[0] != cursorVersion {
		return nil, 0, ErrCursorMalformed
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(raw[:cursorBodyLen])
	want := mac.Sum(nil)
	if subtle.ConstantTimeCompare(raw[cursorBodyLen:], want) != 1 {
		return nil, 0, ErrCursorMalformed
	}
	id := make([]byte, cursorIDLen)
	copy(id, raw[1:1+cursorIDLen])
	pos := int64(binary.BigEndian.Uint64(raw[1+cursorIDLen : cursorBodyLen]))
	if pos < 0 {
		return nil, 0, ErrCursorMalformed
	}
	return id, pos, nil
}
