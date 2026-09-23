// collection.go 提供并发安全的段集合：查询走一致快照，合并崩溃安全。
package merge

import (
	"os"
	"path/filepath"
	"sort"
	"sync"

	"ontology/phrase"
	"ontology/posting"
	"ontology/segment"
)

// Collection 是可查询、可合并的段集合。段与删除位图均写时复制，
// 查询永远作用于某一个一致的段集合。
type Collection struct {
	mu      sync.RWMutex
	segs    []*segment.Segment
	deleted map[uint32]bool
	nextDoc uint32
	dir     string
}

// New 创建空集合；dir 非空时 Compact 会落盘。
func New(dir string) *Collection {
	return &Collection{deleted: map[uint32]bool{}, dir: dir}
}

// Recover 打开 dir 中的段集合：清理崩溃残留的 *.tmp，加载全部 *.seg。
func Recover(dir string) (*Collection, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	c := New(dir)
	var files []string
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) == ".tmp" {
			os.Remove(filepath.Join(dir, name)) // 半截新段：识别并清理
			continue
		}
		if filepath.Ext(name) == ".seg" {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	for _, f := range files {
		s, err := segment.Read(filepath.Join(dir, f))
		if err != nil {
			return nil, err
		}
		c.segs = append(c.segs, s)
		for _, t := range s.Terms {
			for _, p := range s.Lists[t] {
				if p.Doc >= c.nextDoc {
					c.nextDoc = p.Doc + 1
				}
			}
		}
	}
	return c, nil
}

// AddDoc 把一篇词元序列加入集合，返回全局文档号。
func (c *Collection) AddDoc(tokens []string) uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	doc := c.nextDoc
	c.nextDoc++
	bs := map[string]*posting.Builder{}
	for i, tok := range tokens {
		b, ok := bs[tok]
		if !ok {
			b = &posting.Builder{}
			bs[tok] = b
		}
		_ = b.Add(doc, uint32(i))
	}
	lists := map[string]posting.List{}
	for t, b := range bs {
		lists[t] = b.List()
	}
	segs := make([]*segment.Segment, len(c.segs)+1)
	copy(segs, c.segs)
	segs[len(c.segs)] = segment.New(lists)
	c.segs = segs
	return doc
}

// Delete 标记文档已删除（合并前查询用位图过滤）。
func (c *Collection) Delete(doc uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	nd := make(map[uint32]bool, len(c.deleted)+1)
	for d, v := range c.deleted {
		nd[d] = v
	}
	nd[doc] = true
	c.deleted = nd
}

func (c *Collection) snapshot() ([]*segment.Segment, map[uint32]bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.segs, c.deleted
}

// Phrase 在一致快照上执行短语查询，过滤已删除文档。
func (c *Collection) Phrase(terms ...string) []phrase.Hit {
	segs, deleted := c.snapshot()
	var out []phrase.Hit
	for _, s := range segs {
		lists := make([]posting.List, len(terms))
		for i, t := range terms {
			lists[i], _ = s.Lookup(t)
		}
		for _, h := range phrase.Phrase(lists, nil) {
			if !deleted[h.Doc] {
				out = append(out, h)
			}
		}
	}
	return out
}

// And 在一致快照上执行布尔 AND，过滤已删除文档。
func (c *Collection) And(terms ...string) []uint32 {
	segs, deleted := c.snapshot()
	var out []uint32
	for _, s := range segs {
		lists := make([]posting.List, len(terms))
		for i, t := range terms {
			lists[i], _ = s.Lookup(t)
		}
		for _, d := range phrase.And(lists, nil) {
			if !deleted[d] {
				out = append(out, d)
			}
		}
	}
	return out
}

// Compact 把当前全部段合并为一段：先写 *.tmp，原子 rename 为 *.seg，
// 再换入内存视图并删除旧段文件。中途崩溃时旧段仍可用。
func (c *Collection) Compact() error {
	segs, deleted := c.snapshot()
	if len(segs) == 0 {
		return nil
	}
	var st Stats
	merged := Merge(segs, deleted, &st)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dir != "" {
		tmp := filepath.Join(c.dir, "merged.seg.tmp")
		if err := segment.Write(tmp, merged); err != nil {
			return err
		}
		final := filepath.Join(c.dir, "merged.seg")
		if err := os.Rename(tmp, final); err != nil {
			return err
		}
		entries, err := os.ReadDir(c.dir)
		if err != nil {
			return err
		}
		for _, e := range entries { // 合并落盘成功后删除旧段文件
			if n := e.Name(); n != "merged.seg" && filepath.Ext(n) == ".seg" {
				os.Remove(filepath.Join(c.dir, n))
			}
		}
	}
	c.segs = []*segment.Segment{merged}
	return nil
}

// Segments 返回当前段数（测试用）。
func (c *Collection) Segments() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.segs)
}
