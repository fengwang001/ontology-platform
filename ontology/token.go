package ontology

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// 续读标记是「已返回对象集合」在固定遍历顺序下位置的自描述快照，
// 形如 "ont1.<base64(payload)>.<base64(mac)>"。
// 它不依赖服务端会话：重复使用同一标记必定重放同一页内容；
// 签名防止调用方伪造版本号、栈帧或参数。

const tokenPrefix = "ont1."

// continuationToken 是续读标记的内部明文结构。
type continuationToken struct {
	StoreID    string              `json:"sid"`
	Version    int64               `json:"v"`
	Start      string              `json:"s"`
	MaxDepth   int                 `json:"d"`
	Fanout     int                 `json:"f"`
	PageSize   int                 `json:"ps"`
	Stack      []frame             `json:"st"`
	Excluded   map[string]struct{} `json:"ex"`
	EmittedSet map[string]struct{} `json:"em"`
	Emitted    int                 `json:"n"`
	Trunc      Truncation          `json:"t"`
	Started    bool                `json:"z"`
}

// frame 是 DFS 栈帧，仅记录待发射节点与其深度。
type frame struct {
	Node  string `json:"n"`
	Depth int    `json:"d"`
}

type tokenCodec struct {
	key []byte
}

func newTokenCodec() *tokenCodec {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// 随机源不可用时退化为全固定密钥仍能防篡改，仅失去跨进程不可伪造性。
		key = []byte("ontology-fallback-key-0123456789ab")
	}
	return &tokenCodec{key: key}
}

func (c *tokenCodec) mac(payload []byte) []byte {
	m := hmac.New(sha256.New, c.key)
	m.Write(payload)
	return m.Sum(nil)
}

func (c *tokenCodec) encode(t *continuationToken) string {
	raw, err := json.Marshal(t)
	if err != nil {
		return ""
	}
	payload := make([]byte, base64.RawURLEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(payload, raw)
	sig := c.mac(payload)
	out := make([]byte, base64.RawURLEncoding.EncodedLen(len(sig)))
	base64.RawURLEncoding.Encode(out, sig)
	return tokenPrefix + string(payload) + "." + string(out)
}

func (c *tokenCodec) decode(s string) (*continuationToken, error) {
	if len(s) <= len(tokenPrefix) || s[:len(tokenPrefix)] != tokenPrefix {
		return nil, fmt.Errorf("%w: bad continuation token prefix", ErrInvalidArgument)
	}
	body := s[len(tokenPrefix):]
	dot := -1
	for i := len(body) - 1; i >= 0; i-- {
		if body[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		return nil, fmt.Errorf("%w: malformed continuation token", ErrInvalidArgument)
	}
	payload := []byte(body[:dot])
	sigB64 := body[dot+1:]
	want := c.mac(payload)
	got, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || subtle.ConstantTimeCompare(want, got) != 1 {
		return nil, fmt.Errorf("%w: continuation token signature invalid", ErrInvalidArgument)
	}
	raw := make([]byte, base64.RawURLEncoding.DecodedLen(len(payload)))
	n, err := base64.RawURLEncoding.Decode(raw, payload)
	if err != nil {
		return nil, fmt.Errorf("%w: continuation token payload invalid", ErrInvalidArgument)
	}
	var t continuationToken
	if err := json.Unmarshal(raw[:n], &t); err != nil {
		return nil, fmt.Errorf("%w: continuation token json invalid", ErrInvalidArgument)
	}
	if t.MaxDepth < 0 || t.Fanout < 0 || t.PageSize <= 0 || t.Start == "" {
		return nil, fmt.Errorf("%w: continuation token fields out of range", ErrInvalidArgument)
	}
	return &t, nil
}
