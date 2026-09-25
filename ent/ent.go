// Package ent 定义单条审计记录、字段拼接与哈希计算。它不依赖其他包。
package ent

import (
	"crypto/sha256"
	"encoding/binary"
)

// sep 是字段拼接时使用的固定分隔符；字符串字段另附 8 字节长度前缀以消除歧义。
var sep = []byte{0x1f}

// Entry 是一条不可变的审计记录。
type Entry struct {
	Seq  int64
	TS   int64
	Who  string
	Op   string
	Hash [32]byte
}

// GenesisHash 返回创世条目的固定哈希 sha256("genesis")。
func GenesisHash() [32]byte {
	return sha256.Sum256([]byte("genesis"))
}

// Genesis 构造创世条目（Seq=0, TS=0, Who="genesis", Op="init"）。
// 其 Hash 为固定值 GenesisHash，不经过哈希链计算。
func Genesis() Entry {
	return Entry{Seq: 0, TS: 0, Who: "genesis", Op: "init", Hash: GenesisHash()}
}

// appendStr 以「长度前缀 + 分隔符 + 字节 + 分隔符」追加字符串，保证拼接无歧义。
func appendStr(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint64(b, uint64(len(s)))
	b = append(b, sep...)
	b = append(b, s...)
	b = append(b, sep...)
	return b
}

// NextHash 按 sha256(prev ‖ Seq ‖ TS ‖ Who ‖ Op) 计算条目哈希，
// 各字段用固定分隔符拼接，整数用大端定长编码，字符串带长度前缀。
func NextHash(prev [32]byte, e Entry) [32]byte {
	b := make([]byte, 0, 32+8+8+len(e.Who)+len(e.Op)+16)
	b = append(b, prev[:]...)
	b = append(b, sep...)
	b = binary.BigEndian.AppendUint64(b, uint64(e.Seq))
	b = append(b, sep...)
	b = binary.BigEndian.AppendUint64(b, uint64(e.TS))
	b = append(b, sep...)
	b = appendStr(b, e.Who)
	b = appendStr(b, e.Op)
	return sha256.Sum256(b)
}
