package bencode

// 解码出的值树使用如下 Go 类型表示：
//
//	整数   -> int64
//	字节串 -> []byte
//	列表   -> []any
//	字典   -> Dict（保持键的原始顺序）

// Pair 是字典中的一个键值对。
type Pair struct {
	Key   []byte
	Value any
}

// Dict 是有序字典：键按原始字节序严格升序排列。
type Dict []Pair
