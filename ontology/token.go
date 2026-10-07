package ontology

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// continuationToken 是续读标记的内部载荷。
// 它锚定：快照版本、起点、遍历参数以及“已返回对象数”在固定遍历顺序中的偏移。
type continuationToken struct {
	Epoch     uint64   `json:"e"`
	Start     ObjectID `json:"s"`
	MaxDepth  int      `json:"d"`
	MaxFanout int      `json:"f"`
	Offset    int      `json:"o"`
}

var tokenEncoding = base64.RawURLEncoding

func signToken(key []byte, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return mac.Sum(nil)
}

// encodeToken 将载荷序列化为防篡改字符串：base64(json).base64(hmac)。
func encodeToken(key []byte, tok continuationToken) (string, error) {
	raw, err := json.Marshal(tok)
	if err != nil {
		return "", err
	}
	payload := tokenEncoding.EncodeToString(raw)
	sig := tokenEncoding.EncodeToString(signToken(key, raw))
	return payload + "." + sig, nil
}

// decodeToken 校验格式与 HMAC，任何不一致都返回 ErrTokenInvalid。
func decodeToken(key []byte, encoded string) (continuationToken, error) {
	var tok continuationToken
	// base64url 字母表含 '-'，因此只能定位点分隔符本身，不能把 '-' 当作候选。
	dot := strings.IndexByte(encoded, '.')
	if dot <= 0 || dot == len(encoded)-1 || strings.IndexByte(encoded[dot+1:], '.') >= 0 {
		return tok, ErrTokenInvalid
	}
	raw, err := tokenEncoding.DecodeString(encoded[:dot])
	if err != nil {
		return tok, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}
	sig, err := tokenEncoding.DecodeString(encoded[dot+1:])
	if err != nil {
		return tok, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}
	if !hmac.Equal(sig, signToken(key, raw)) {
		return tok, ErrTokenInvalid
	}
	if err := json.Unmarshal(raw, &tok); err != nil {
		return tok, fmt.Errorf("%w: %v", ErrTokenInvalid, err)
	}
	if tok.Offset < 0 || tok.MaxDepth < 0 || tok.MaxFanout < 0 || tok.Start == "" {
		return tok, ErrTokenInvalid
	}
	return tok, nil
}
