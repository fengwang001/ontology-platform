package ws

type Item struct {
	OrigPos int
	Byte    byte
}

type Buffer struct {
	items []Item
}

func New() *Buffer {
	return &Buffer{}
}

func (b *Buffer) Add(pos int, value byte) {
	b.items = append(b.items, Item{OrigPos: pos, Byte: value})
}

func (b *Buffer) Len() int {
	return len(b.items)
}

func (b *Buffer) Clear() {
	b.items = b.items[:0]
}

func (b *Buffer) Items() []Item {
	return b.items
}

func (b *Buffer) Bytes() []byte {
	out := make([]byte, len(b.items))
	for i, item := range b.items {
		out[i] = item.Byte
	}
	return out
}

func IsTrailing(byteValue byte) bool {
	return byteValue == ' ' || byteValue == '\t'
}
