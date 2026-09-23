package durable

import (
	"encoding/binary"
	"hash/crc32"
)

// RecLen 是持久记录的字节长度，供测试与文档核对。
const RecLen = recLen

// Writes 返回自打开以来成功/尝试的持久写次数（含失败注入）。
func (c *Counter) Writes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes
}

// FailNextWrites 令随后 k 次 persist 失败（测试故障注入）。
func (c *Counter) FailNextWrites(k int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failWrites = k
}

// Encode 暴露记录编码，供损坏测试构造文件。
func Encode(v uint64) []byte {
	var rec [recLen]byte
	rec[0] = magicByte
	binary.BigEndian.PutUint64(rec[1:9], v)
	binary.BigEndian.PutUint32(rec[crcOff:], crc32.ChecksumIEEE(rec[:crcOff]))
	out := make([]byte, recLen)
	copy(out, rec[:])
	return out
}
