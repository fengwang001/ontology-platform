package ontology

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// 游标格式：v1.<traversalID>.<seq>.<hmac-hex>
// 游标编码的位置（traversalID + 已返回页序号 seq）相对于遍历开始时
// 钉住的快照版本解释，不随快照之后的并发修改而改变含义。
// HMAC 防止调用方伪造或篡改游标。

const cursorVersion = "v1"

// encodeCursor 生成第 seq 页之后（即下一页）的游标。
func encodeCursor(key []byte, tid string, seq int) string {
	body := cursorVersion + "." + tid + "." + strconv.Itoa(seq)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(body))
	return body + "." + hex.EncodeToString(mac.Sum(nil))[:16]
}

// decodeCursor 解析并校验游标，返回遍历 ID 与页序号。
func decodeCursor(key []byte, s string) (tid string, seq int, err error) {
	parts := strings.Split(s, ".")
	if len(parts) != 4 || parts[0] != cursorVersion {
		return "", 0, fmt.Errorf("malformed cursor")
	}
	seq, err = strconv.Atoi(parts[2])
	if err != nil || seq < 0 {
		return "", 0, fmt.Errorf("malformed cursor seq")
	}
	expected := encodeCursor(key, parts[1], seq)
	if !hmac.Equal([]byte(expected), []byte(s)) {
		return "", 0, fmt.Errorf("cursor signature mismatch")
	}
	return parts[1], seq, nil
}
