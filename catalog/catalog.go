package catalog

import "sync"

// Class 为目录类别。
type Class int

const (
	Jia  Class = iota // 甲类
	Yi                // 乙类
	Bing              // 丙类
)

// Item 为一条目录。
type Item struct {
	Code  string
	Class Class
	P     int64 // 乙类先行自付百分数
	Limit int64 // 限价，0 表示无限价
}

// Catalog 为医保目录。
type Catalog struct {
	mu    sync.RWMutex
	items map[string]Item
}

// New 创建空目录。
func New() *Catalog {
	return &Catalog{items: make(map[string]Item)}
}

// AddItem 新增或覆盖目录项（骨架）。
func (c *Catalog) AddItem(code string, class Class, p, limit int64) error {
	if code == "" {
		return ErrInvalidCode
	}
	if class != Jia && class != Yi && class != Bing {
		return ErrInvalidClass
	}
	if p < 0 || p > 100 {
		return ErrInvalidP
	}
	if limit < 0 || limit > 1_000_000_000_000_000_000 {
		return ErrInvalidLimit
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[code] = Item{Code: code, Class: class, P: p, Limit: limit}
	return nil
}

// Get 查询目录项。
func (c *Catalog) Get(code string) (Item, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	it, ok := c.items[code]
	return it, ok
}
