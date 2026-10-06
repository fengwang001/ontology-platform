package ftl

// pageState 页状态：空闲、有效、失效。
type pageState uint8

const (
	pageFree pageState = iota
	pageValid
	pageInvalid
)

// block 擦除块。页只能按页号递增顺序编程（nextFree 单调前进），
// 只有整体擦除后 nextFree 归零才能再次编程。
type block struct {
	pages    []pageState // 每页状态
	lpn      []uint64    // 每页承载的逻辑页号（有效页）
	data     [][]byte    // 每页数据
	nextFree int         // 下一个可编程页号
	valid    int         // 有效页数
	erases   int         // 累计擦除次数
	retired  bool        // 寿命耗尽后退役
}

func newBlock(pagesPerBlock int) block {
	return block{
		pages: make([]pageState, pagesPerBlock),
		lpn:   make([]uint64, pagesPerBlock),
		data:  make([][]byte, pagesPerBlock),
	}
}

// full 块是否已写满。不变式：任一时刻至多一个部分编程的块（活动块），
// 因此非活动块只会是空闲、写满或退役三种形态之一。
func (b *block) full() bool { return b.nextFree == len(b.pages) }

// pa 物理地址。
type pa struct {
	block int
	page  int
}
