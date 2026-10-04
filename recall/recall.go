// Package recall 管理召回合约、到期序与强制买入记录。
//
// Book 是跨标的的全局召回册，始终按 (DL, ID) 升序维护，
// 入口结算时从头取出所有 DL<=now 的合约即为强制买入次序。
package recall

// CContract 是一笔从普通合约拆出的召回合约。
// 截止时刻 DL 之后的第一次入口结算将对剩余量强制买入。
type CContract struct {
	ID       int64
	Lender   []byte
	Borrower []byte
	Sym      []byte
	Qty      int64
	DL       int64
}

// Buy 是一笔到期强制买入的入账记录。
type Buy struct {
	ID     int64
	Qty    int64
	Price  int64
	Pen    int64
	Amount int64
	Time   int64
}

// Book 保存全部未了结召回合约，顺序恒为 (DL, ID) 升序。
type Book struct {
	order []int64
	live  map[int64]*CContract
}

func NewBook() *Book {
	return &Book{live: make(map[int64]*CContract)}
}

// Add 按 (DL, ID) 序插入一笔召回合约。
func (b *Book) Add(c *CContract) {
	b.live[c.ID] = c
	pos := 0
	for pos < len(b.order) {
		x := b.live[b.order[pos]]
		if x.DL > c.DL || (x.DL == c.DL && x.ID > c.ID) {
			break
		}
		pos++
	}
	b.order = append(b.order, 0)
	copy(b.order[pos+1:], b.order[pos:])
	b.order[pos] = c.ID
}

// Get 返回某召回合约（不存在返回 nil）。
func (b *Book) Get(id int64) *CContract {
	return b.live[id]
}

// Remove 删除一笔已了结的召回合约，保持顺序。
func (b *Book) Remove(id int64) {
	if _, ok := b.live[id]; !ok {
		return
	}
	delete(b.live, id)
	for i, v := range b.order {
		if v == id {
			b.order = append(b.order[:i], b.order[i+1:]...)
			return
		}
	}
}

// Due 按 (DL, ID) 序返回所有 dl<=now 的合约（不移除）。
func (b *Book) Due(now int64) []*CContract {
	var out []*CContract
	for _, id := range b.order {
		c := b.live[id]
		if c.DL > now {
			break
		}
		out = append(out, c)
	}
	return out
}

// All 返回全部未了结召回合约的快照，顺序为 (DL, ID) 升序。
func (b *Book) All() []*CContract {
	out := make([]*CContract, 0, len(b.order))
	for _, id := range b.order {
		out = append(out, b.live[id])
	}
	return out
}
