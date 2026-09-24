// Package wire 定义 LZ77 自定义压缩流的字节格式。
package wire

const (
	// Tag 常量，占记录首字节低 2 位。
	TagLit byte = 0
	TagPtr byte = 1
	TagFlush byte = 2
	TagEnd byte = 3
)

// MaxVarintLen 是无符号 64 位变长整数的最大合法字节数。
const MaxVarintLen = 10

// AppendUvar 把 v 以 LEB128 追加到 b。
func AppendUvar(b []byte, v uint64) []byte {
	return b
}

// ReadUvar 从 b 的偏移 off 处读取一个 LEB128。
// 返回值、新偏移；错误为超长/溢出。
func ReadUvar(b []byte, off int) (uint64, int, error) {
	return 0, off, nil
}
