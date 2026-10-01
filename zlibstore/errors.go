// Package zlibstore 实现仅使用 DEFLATE 存储块（stored block，BTYPE=00）的
// zlib（RFC 1950）容器流式编码器与解码器。
//
// 容器字节布局：
//
//	2 字节 zlib 头部（固定 78 01）
//	若干存储块（RFC 1951 第 3.2.4 节）
//	4 字节 Adler-32 校验和（大端，RFC 1950 第 8 节）
package zlibstore

import "errors"

// 编码器可能返回的错误。
var (
	// ErrClosed 表示编码器已经 Close，之后再调用 Write 或 Close 被拒绝。
	ErrClosed = errors.New("zlibstore: encoder already closed")
	// ErrWriter 表示底层 io.Writer 返回了错误，编码器随之进入粘滞失败态。
	ErrWriter = errors.New("zlibstore: underlying writer failure")
)

// 解码器可能返回的错误，彼此可通过 errors.Is 区分。
var (
	// ErrPoisoned 表示解码器此前已发生错误，进入粘滞失败态；
	// 此后任何 Write/Close 均返回本错误且不改变状态。
	ErrPoisoned = errors.New("zlibstore: decoder poisoned")

	// ErrBadMethod CMF 低 4 位压缩方法不为 8（deflate）。
	ErrBadMethod = errors.New("zlibstore: bad compression method (CM must be 8)")
	// ErrBadWindow CMF 高 4 位窗口信息 CINFO 大于 7。
	ErrBadWindow = errors.New("zlibstore: window size too large (CINFO > 7)")
	// ErrBadCheckValue 头两字节按大端解释的 16 位整数不被 31 整除。
	ErrBadCheckValue = errors.New("zlibstore: header check value not divisible by 31")
	// ErrDictionaryPresent FLG 的 FDICT（0x20）位被置位，不支持预置字典。
	ErrDictionaryPresent = errors.New("zlibstore: preset dictionary not supported")

	// ErrBlockHeaderReserved 块头高 5 位非零。
	ErrBlockHeaderReserved = errors.New("zlibstore: reserved bits set in block header")
	// ErrBlockFixedHuffman 块类型为 01（固定 Huffman），不支持。
	ErrBlockFixedHuffman = errors.New("zlibstore: fixed Huffman blocks are not supported")
	// ErrBlockDynamicHuffman 块类型为 10（动态 Huffman），不支持。
	ErrBlockDynamicHuffman = errors.New("zlibstore: dynamic Huffman blocks are not supported")
	// ErrBlockReservedType 块类型为 11（保留/非法）。
	ErrBlockReservedType = errors.New("zlibstore: reserved block type 11")

	// ErrBadNLEN 块的 NLEN 不等于 LEN 的按位取反低 16 位。
	ErrBadNLEN = errors.New("zlibstore: NLEN is not ones complement of LEN")

	// ErrTrailingBytes 4 字节 Adler-32 尾部之后仍有多余字节。
	ErrTrailingBytes = errors.New("zlibstore: trailing bytes after Adler-32")
	// ErrChecksum Adler-32 尾部与已交付数据的校验和不符。
	ErrChecksum = errors.New("zlibstore: Adler-32 checksum mismatch")
	// ErrTruncated Close 时仍未读完整头部、块或 4 字节尾部。
	ErrTruncated = errors.New("zlibstore: truncated stream")
)
