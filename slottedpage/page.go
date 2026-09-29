package slottedpage

import "sync"

// 页头前 28 字节为元数据，其余保留字节始终为零。
// 槽项前 8 字节为 offset/length，其余保留字节始终为零。
// length==0 表示空槽；记录起点恒大于 0，offset==0 无歧义。
const (
	minHeaderSize = 28
	minSlotSize   = 8

	offMagic       = 0
	offPageSize    = 4
	offHeaderSize  = 8
	offSlotSize    = 12
	offSlotCount   = 16
	offFreeEnd     = 20
	offCompactions = 24
)

var magic = [4]byte{'S', 'P', 'G', '1'}

// Page 是固定字节大小的槽式记录页。
//
// 页内布局（低地址在前）：
//
//	[0, headerSize)      页头
//	[headerSize, dirEnd) 槽目录，自前向后增长，每项 slotSize 字节
//	[dirEnd, freeEnd)    连续空闲区
//	[freeEnd, pageSize)  存活记录区，记录自页尾向前紧挨放置
//
// 删除中间记录会在记录区留下空洞；连续空闲区不足而全页可用字节
// 足够时，通过一次整理消除空洞。
type Page struct {
	mu  sync.RWMutex
	buf []byte

	pageSize   int
	headerSize int
	slotSize   int

	slotCount   int
	freeEnd     int
	recordBytes int
	compactions int
}

// Config 描述页的创建参数。HeaderSize 不得小于 28，SlotSize 不得小于 8。
type Config struct {
	PageSize   int
	HeaderSize int
	SlotSize   int
}

// Stats 为对外报告的页内账目与整理次数。
//
//	FreeBytes  = PageSize - HeaderSize - SlotCount*SlotSize - RecordBytes
//	ContigFree = FreeEnd - (HeaderSize + SlotCount*SlotSize)
//
// FreeBytes >= ContigFree，差额即记录区内的碎片空洞。
type Stats struct {
	PageSize    int
	HeaderSize  int
	SlotSize    int
	SlotCount   int
	LiveRecords int
	SlotBytes   int
	RecordBytes int
	FreeBytes   int
	FreeStart   int
	FreeEnd     int
	ContigFree  int
	Compactions int
}
