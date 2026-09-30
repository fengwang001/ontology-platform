package extent

import "sync"

// Volume 持有一个物理分配器与一组命名文件。
// 不同文件的操作可并发；同一文件的修改通过文件锁串行化。
type Volume struct {
	alloc *allocator
	mu    sync.RWMutex
	files map[string]*File
}

// NewVolume 创建容量为 capacity 字节的卷。
func NewVolume(capacity int64) *Volume {
	return &Volume{
		alloc: newAllocator(capacity),
		files: make(map[string]*File),
	}
}

// CreateFile 注册一个长度上限为 limit 的空文件。
func (v *Volume) CreateFile(name string, limit int64) (*File, error) {
	if limit <= 0 {
		return nil, ErrEmptyRange
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	f := &File{vol: v, name: name, limit: limit, len: limit}
	v.files[name] = f
	return f, nil
}

// File 按名查找文件。
func (v *Volume) File(name string) (*File, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	f, ok := v.files[name]
	if !ok {
		return nil, ErrUnknownFile
	}
	return f, nil
}

// Used 返回已用物理字节数（引用计数非零的字节数）。
func (v *Volume) Used() int64 {
	v.alloc.mu.Lock()
	defer v.alloc.mu.Unlock()
	return v.alloc.usedLocked()
}

// Write 用 data 覆盖 f 的 [off, off+len(data))。
// 先为整个区间分配新物理空间，再释放被覆盖的旧物理空间。
func (v *Volume) Write(f *File, off int64, data []byte) error {
	n := int64(len(data))
	if n <= 0 {
		return ErrEmptyRange
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if off < 0 || off+n > f.len {
		return ErrOutOfBounds
	}
	// 先分配新物理空间；失败时不改变任何状态。
	phys := v.alloc.allocWrite(data)
	if phys < 0 {
		return ErrNoSpace
	}
	// 再解除被覆盖的旧映射并释放其物理空间。
	freed := f.unmapLocked(off, off+n)
	for _, p := range freed {
		v.alloc.release(p.phys, p.length)
	}
	f.insertLocked(extent{start: off, length: n, phys: phys})
	return nil
}

// Clone 把 src 的 [srcOff, srcOff+n) 的映射复制到 dst 的 dstOff 处，
// 只共享物理空间并增加引用计数；dst 原有内容按覆盖处理；
// 源中的空洞在目标中仍是空洞。
func (v *Volume) Clone(src *File, srcOff int64, dst *File, dstOff, n int64) error {
	if n <= 0 {
		return ErrEmptyRange
	}
	if src == dst {
		if srcOff < dstOff+n && dstOff < srcOff+n {
			return ErrOverlap
		}
		src.mu.Lock()
		defer src.mu.Unlock()
		if err := checkRange(src, srcOff, n); err != nil {
			return err
		}
		if err := checkRange(dst, dstOff, n); err != nil {
			return err
		}
		v.cloneLocked(src, srcOff, dst, dstOff, n)
		return nil
	}
	// 跨文件克隆按固定顺序加锁，避免死锁。
	first, second := src, dst
	if dst.name < src.name {
		first, second = dst, src
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	second.mu.Lock()
	defer second.mu.Unlock()
	if err := checkRange(src, srcOff, n); err != nil {
		return err
	}
	if err := checkRange(dst, dstOff, n); err != nil {
		return err
	}
	v.cloneLocked(src, srcOff, dst, dstOff, n)
	return nil
}

// checkRange 校验区间落在文件当前长度内。
func checkRange(f *File, off, n int64) error {
	if off < 0 || off+n > f.len {
		return ErrOutOfBounds
	}
	return nil
}

// cloneLocked 在源与目标文件锁均已持有时执行克隆。
// 先为共享片段增加引用计数，再释放目标被覆盖的旧空间。
func (v *Volume) cloneLocked(src *File, srcOff int64, dst *File, dstOff, n int64) {
	// 收集源区间内的映射片段，空洞不产生片段。
	var pieces []extent
	for _, e := range src.exts {
		if e.end() <= srcOff || e.start >= srcOff+n {
			continue
		}
		lo, hi := e.start, e.end()
		if lo < srcOff {
			lo = srcOff
		}
		if hi > srcOff+n {
			hi = srcOff + n
		}
		pieces = append(pieces, extent{
			start:  dstOff + (lo - srcOff),
			length: hi - lo,
			phys:   e.phys + (lo - e.start),
		})
	}
	// 共享物理空间：只增加引用计数。
	for _, p := range pieces {
		v.alloc.share(p.phys, p.length)
	}
	// 目标原有内容按覆盖处理。
	freed := dst.unmapLocked(dstOff, dstOff+n)
	for _, p := range freed {
		v.alloc.release(p.phys, p.length)
	}
	for _, p := range pieces {
		dst.insertLocked(p)
	}
}

// Truncate 把 f 截断到 newLen，释放超出部分。
func (v *Volume) Truncate(f *File, newLen int64) error {
	if newLen < 0 || newLen > f.limit {
		return ErrOutOfBounds
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	freed := f.unmapLocked(newLen, f.len)
	for _, p := range freed {
		v.alloc.release(p.phys, p.length)
	}
	f.len = newLen
	return nil
}

// Read 读出 f 的 [off, off+n)；未映射区间读出零字节。
func (v *Volume) Read(f *File, off, n int64) ([]byte, error) {
	if n <= 0 {
		return nil, ErrEmptyRange
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if err := checkRange(f, off, n); err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	for _, e := range f.exts {
		if e.end() <= off || e.start >= off+n {
			continue
		}
		lo, hi := e.start, e.end()
		if lo < off {
			lo = off
		}
		if hi > off+n {
			hi = off + n
		}
		srcStart := e.phys + (lo - e.start)
		copy(buf[lo-off:hi-off], v.alloc.data[srcStart:srcStart+(hi-lo)])
	}
	return buf, nil
}
