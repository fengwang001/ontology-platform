package segmap

import (
	"errors"
	"log"
	"os"
	"sort"
	"sync"
)

// stdLogger 用标准 log 包打印每次操作的输入、输出与判定依据。
type stdLogger struct {
	l *log.Logger
}

func (s stdLogger) logf(format string, args ...any) {
	s.l.Printf(format, args...)
}

// Store 是支持克隆共享的文件区段映射器。
type Store struct {
	phys    *physical
	filesMu sync.Mutex
	files   map[string]*file
	log     logger
}

// New 创建容量为 physicalBytes 的映射器；未映射逻辑字节读出零且不占空间。
func New(physicalBytes int64) *Store {
	return &Store{
		phys:  newPhysical(physicalBytes),
		files: make(map[string]*file),
		log:   stdLogger{l: log.New(os.Stderr, "[segmap] ", log.LstdFlags|log.Lmicroseconds)},
	}
}

// SetLogger 替换操作日志输出（测试可注入缓冲 logger）。
func (s *Store) SetLogger(l Logger) {
	if l != nil {
		s.log = adaptLogger{l}
	}
}

// Logger 是外部日志接口，与内部 logger 解耦。
type Logger interface {
	Printf(format string, args ...any)
}

type adaptLogger struct{ l Logger }

func (a adaptLogger) logf(format string, args ...any) { a.l.Printf(format, args...) }

var errFileNotFound = errors.New("segmap: file not found")

// CreateFile 创建一个长度为 0 的空文件；重名创建被拒绝。
func (s *Store) CreateFile(name string) error {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	if _, ok := s.files[name]; ok {
		s.log.logf("CreateFile(name=%q) -> error: file already exists", name)
		return errors.New("segmap: file already exists: " + name)
	}
	s.files[name] = &file{name: name}
	s.log.logf("CreateFile(name=%q) -> ok length=0 extents=[]", name)
	return nil
}

func (s *Store) getFile(name string) (*file, error) {
	f, ok := s.files[name]
	if !ok {
		return nil, errFileNotFound
	}
	return f, nil
}

// validateInterval 校验左闭右开区间非空、不颠倒且不越过文件长度上限。
func validateInterval(start, length int64) error {
	if start < 0 || length <= 0 || start > MaxFileLength-length {
		if start < 0 || length <= 0 || start+length < start {
			return ErrInvalidRange
		}
		return ErrFileTooLarge
	}
	return nil
}

// Write 在 name 的 [offset, offset+len(data)) 写入数据。
// 语义：先为整个新区间分配新物理空间，再释放被覆盖的旧映射；
// 部分重叠的旧区段被劈开，未覆盖部分保留；写后可能触发相接合并。
func (s *Store) Write(name string, offset int64, data []byte) error {
	length := int64(len(data))
	f, err := s.getFile(name)
	if err != nil {
		s.log.logf("Write(file=%q offset=%d len=%d) -> error: %v", name, offset, length, err)
		return err
	}
	if err := validateInterval(offset, length); err != nil {
		s.log.logf("Write(file=%q offset=%d len=%d) -> reject: %v（判定：区间为空/颠倒/越上限，整体拒绝）", name, offset, length, err)
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// 第一步：整体分配；空间不足时在触碰任何映射前拒绝。
	physStart, ok := s.phys.allocate(length)
	if !ok {
		s.log.logf("Write(file=%q offset=%d len=%d) -> reject: %v（判定：first-fit 无 %d 连续空闲字节）", name, offset, length, ErrSpaceExhausted, length)
		return ErrSpaceExhausted
	}

	// 第二步：新映射先生效（计数已含新分配的 1），再释放被覆盖部分。
	old := f.extents
	oldCopy := append([]extent(nil), old...)
	f.extents, _ = releaseRange(f.extents, offset, offset+length)
	f.extents = insertExtent(f.extents, extent{
		logicalStart:  offset,
		physicalStart: physStart,
		length:        length,
	})
	copy(s.phys.data[physStart:physStart+length], data)

	// 第三步：对被覆盖的旧物理字节计数 -1，归零即回收。
	releasedBytes := releaseMappedRuns(s.phys, oldCopy, offset, offset+length)
	if offset+length > f.length {
		f.length = offset + length
	}
	s.log.logf("Write(file=%q offset=%d len=%d) -> ok physStart=%d releasedOld=%d used=%d extents=%v",
		name, offset, length, physStart, releasedBytes, s.phys.usedBytes(), describeExtents(f.extents))
	return nil
}

// releaseMappedRuns 释放 oldExtents 中落在 [start,end) 内的物理片段。
func releaseMappedRuns(p *physical, oldExtents []extent, start, end int64) int64 {
	var n int64
	for _, e := range intersectExtents(oldExtents, start, end) {
		p.release(e.physicalStart, e.length)
		n += e.length
	}
	return n
}

// Read 读取 [offset, offset+length)。空洞（未映射区间）读出零字节，
// 且不消耗物理空间。读只取共享锁，可与其它读者并发。
func (s *Store) Read(name string, offset, length int64) ([]byte, error) {
	if offset < 0 || length < 0 {
		return nil, ErrInvalidRange
	}
	f, err := s.getFile(name)
	if err != nil {
		s.log.logf("Read(file=%q offset=%d len=%d) -> error: %v", name, offset, length, err)
		return nil, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()

	buf := make([]byte, length)
	if offset >= f.length {
		s.log.logf("Read(file=%q offset=%d len=%d) -> ok zeros（判定：起点越过文件长度 %d，全部读零）", name, offset, length, f.length)
		return buf, nil
	}
	readable := length
	if offset+readable > f.length {
		readable = f.length - offset
	}
	for _, e := range intersectExtents(f.extents, offset, offset+readable) {
		srcOff := e.logicalStart - offset
		copy(buf[srcOff:srcOff+e.length], s.phys.data[e.physicalStart:e.physicalStart+e.length])
	}
	s.log.logf("Read(file=%q offset=%d len=%d) -> ok bytes=%d（判定：相交区段 %d 段拷贝，其余位置为空洞零）",
		name, offset, length, readable, len(intersectExtents(f.extents, offset, offset+readable)))
	return buf, nil
}

// Clone 将 src 的 [srcOffset,srcOffset+length) 复制为 dst 的 dstOffset 起内容。
// 不复制任何物理字节：仅增加共享区段的引用计数；源中的空洞在目标仍是空洞；
// 目标原内容按覆盖处理；同一文件内源/目标区间重叠整体拒绝。
func (s *Store) Clone(src string, srcOffset int64, dst string, dstOffset, length int64) error {
	srcFile, err := s.getFile(src)
	if err != nil {
		s.log.logf("Clone(src=%q srcOff=%d dst=%q dstOff=%d len=%d) -> error: %v", src, srcOffset, dst, dstOffset, length, err)
		return err
	}
	dstFile, err := s.getFile(dst)
	if err != nil {
		s.log.logf("Clone(src=%q srcOff=%d dst=%q dstOff=%d len=%d) -> error: %v", src, srcOffset, dst, dstOffset, length, err)
		return err
	}
	if err := validateInterval(srcOffset, length); err != nil {
		s.log.logf("Clone(...) -> reject: %v（判定：源区间为空/颠倒/越上限）", err)
		return err
	}
	if err := validateInterval(dstOffset, length); err != nil {
		s.log.logf("Clone(...) -> reject: %v（判定：目标区间为空/颠倒/越上限）", err)
		return err
	}
	if srcFile == dstFile && intervalsOverlap(srcOffset, srcOffset+length, dstOffset, dstOffset+length) {
		s.log.logf("Clone(src=%q srcOff=%d dst=%q dstOff=%d len=%d) -> reject: %v（判定：同文件源/目标区间重叠）",
			src, srcOffset, dst, dstOffset, length, ErrSameFileOverlap)
		return ErrSameFileOverlap
	}

	// 固定加锁顺序，避免同文件/双文件克隆相互死锁。
	first, second := srcFile, dstFile
	if srcFile == dstFile {
		second = nil
	} else if dstFile.name < srcFile.name {
		first, second = dstFile, srcFile
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	if second != nil {
		second.mu.Lock()
		defer second.mu.Unlock()
	}

	// 克隆只可能对计数非零字节 +1 或 -1，不分配空间，故无“空间不足”失败点。
	pieces := intersectExtents(srcFile.extents, srcOffset, srcOffset+length)

	// 先增加共享计数，再释放目标被覆盖映射：保证中途不出现可被抢占的空闲假象。
	for _, p := range pieces {
		s.phys.retain(p.physicalStart, p.length)
	}
	oldDst := append([]extent(nil), dstFile.extents...)
	dstFile.extents, _ = releaseRange(dstFile.extents, dstOffset, dstOffset+length)
	for _, p := range pieces {
		dstFile.extents = insertExtent(dstFile.extents, extent{
			logicalStart:  dstOffset + (p.logicalStart - srcOffset),
			physicalStart: p.physicalStart,
			length:        p.length,
		})
	}
	for _, e := range intersectExtents(oldDst, dstOffset, dstOffset+length) {
		s.phys.release(e.physicalStart, e.length)
	}
	if dstOffset+length > dstFile.length {
		dstFile.length = dstOffset + length
	}
	s.log.logf("Clone(src=%q[%d:%d] -> dst=%q@%d) -> ok sharedPieces=%d（含空洞 %d 字节不映射） used=%d extents=%v",
		src, srcOffset, srcOffset+length, dst, dstOffset, len(pieces),
		length-totalExtentLength(pieces), s.phys.usedBytes(), describeExtents(dstFile.extents))
	return nil
}

// Truncate 将文件截断到 newLength：超出新长度的映射被释放；
// 落在中间时仅劈开并保留前缀，文件长度缩小。
func (s *Store) Truncate(name string, newLength int64) error {
	if newLength < 0 || newLength > MaxFileLength {
		s.log.logf("Truncate(file=%q newLen=%d) -> reject: %v", name, newLength, ErrFileTooLarge)
		return ErrFileTooLarge
	}
	f, err := s.getFile(name)
	if err != nil {
		s.log.logf("Truncate(file=%q newLen=%d) -> error: %v", name, newLength, err)
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	if newLength >= f.length {
		f.length = newLength
		s.log.logf("Truncate(file=%q newLen=%d) -> ok（判定：不缩小，仅调整长度）", name, newLength)
		return nil
	}
	old := append([]extent(nil), f.extents...)
	f.extents, _ = releaseRange(f.extents, newLength, f.length)
	removed := intersectExtents(old, newLength, f.length)
	for _, e := range removed {
		s.phys.release(e.physicalStart, e.length)
	}
	f.length = newLength
	s.log.logf("Truncate(file=%q newLen=%d) -> ok released=%d used=%d extents=%v",
		name, newLength, totalExtentLength(removed), s.phys.usedBytes(), describeExtents(f.extents))
	return nil
}

// Length 返回文件逻辑长度。
func (s *Store) Length(name string) int64 {
	f, err := s.getFile(name)
	if err != nil {
		return -1
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.length
}

// UsedPhysicalBytes 返回引用计数非零的物理字节总数。
func (s *Store) UsedPhysicalBytes() int64 { return s.phys.usedBytes() }

func intervalsOverlap(aLo, aHi, bLo, bHi int64) bool { return aLo < bHi && bLo < aHi }

func totalExtentLength(es []extent) int64 {
	var n int64
	for _, e := range es {
		n += e.length
	}
	return n
}

// describeExtents 生成确定性的区段描述，供日志与调试。
func describeExtents(es []extent) [][3]int64 {
	out := make([][3]int64, 0, len(es))
	for _, e := range es {
		out = append(out, [3]int64{e.logicalStart, e.logicalEnd(), e.physicalStart})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}
