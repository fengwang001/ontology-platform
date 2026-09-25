package verify

import (
	"crypto/sha256"
	"encoding/binary"
)

type record struct {
	key   string
	value []byte
}

// decodeRecords 解码一个块体中的全部键值记录。
func decodeRecords(body []byte) ([]record, error) {
	var out []record
	for len(body) > 0 {
		kl, n := binary.Uvarint(body)
		if n <= 0 || uint64(len(body)) < uint64(n)+kl {
			return nil, errBody
		}
		body = body[n:]
		key := string(body[:kl])
		body = body[kl:]
		if len(body) == 0 {
			return nil, errBody
		}
		vl, n := binary.Uvarint(body)
		if n <= 0 || uint64(len(body)) < uint64(n)+vl {
			return nil, errBody
		}
		body = body[n:]
		value := body[:vl]
		body = body[vl:]
		out = append(out, record{key: key, value: append([]byte(nil), value...)})
	}
	return out, nil
}

// canonicalDigest 计算单条规范化记录的 SHA-256。
func canonicalDigest(r record) []byte {
	h := sha256.New()
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(r.key)))
	h.Write(buf[:n])
	h.Write([]byte(r.key))
	n = binary.PutUvarint(buf[:], uint64(len(r.value)))
	h.Write(buf[:n])
	h.Write(r.value)
	return h.Sum(nil)
}

// xorDigest 把一条记录摘要并入 XOR 折叠总校验和（顺序无关）。
func xorDigest(acc []byte, r record) []byte {
	d := canonicalDigest(r)
	if len(acc) == 0 {
		return d
	}
	for i := range acc {
		acc[i] ^= d[i]
	}
	return acc
}
