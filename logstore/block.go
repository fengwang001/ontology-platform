package logstore

// 段内每个块有一个定长头部：
//
//	时间戳 8 字节（uint64 大端）
//	类型   1 字节（0=值块，1=墓碑）
//	键长度 4 字节（uint32 大端）
//	值长度 4 字节（uint32 大端，墓碑为 0）
//
// 头部之后紧跟键字节与值字节。同一条逻辑序列下块尺寸是确定的，
// 因此内存模型直接按该公式逐字节记账，保证账目不依赖具体编码时机。
const blockHeaderSize = uint64(8 + 1 + 4 + 4)

// kind 区分普通值块与墓碑块。
type kind uint8

const (
	kindValue kind = 0
	kindTomb  kind = 1
)

// block 是段内一次追加的不可变记录。
type block struct {
	key   string
	value []byte
	kind  kind
	ts    uint64
}

// loc 是一个块在日志中的物理位置。
type loc struct {
	segID  int
	offset uint64
	size   uint64
	ts     uint64
	kind   kind
}

// blockSize 返回块在段内占用的字节数（定长头 + 键 + 值）。
func blockSize(key string, value []byte, kd kind) uint64 {
	valueLen := uint64(len(value))
	if kd == kindTomb {
		valueLen = 0
	}
	return blockHeaderSize + uint64(len(key)) + valueLen
}
