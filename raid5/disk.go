package raid5

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// BlockSize 是块设备的固定块大小。
const BlockSize = 4096

// Disk 抽象一块成员盘。坐标 (stripe) 即条带号，每块盘每条带占一个块槽位。
// 实现必须保证：WriteBlock 持久化返回后数据落盘；盘失效后所有读写返回错误。
type Disk interface {
	Index() int
	ReadBlock(stripe int, buf []byte) error
	WriteBlock(stripe int, data []byte) error
	// ReadBlockForRebuild 重建读：已重建的块可读，未重建槽返回 ErrDiskDead。
	ReadBlockForRebuild(stripe int, buf []byte) error
	// WriteBlockForRebuild 重建写：把恢复出的块写入换入新盘并标记该槽已重建。
	WriteBlockForRebuild(stripe int, data []byte) error
	// MarkRebuilding 声明该盘是换入的新盘，进入重建状态。
	MarkRebuilding() error
	Rebuilding() bool
	Rebuilt(stripe int) bool
	// FinishRebuild 全部条带重建完成，清除重建标记。
	FinishRebuild() error
	Fail()
	Failed() bool
	Close() error
}

// FileDisk 是基于文件的成员盘实现。
//
// 持久化状态（与数据文件同目录）：
//
//	<path>           数据文件（stripes 个块）
//	<path>.dead      存在表示该盘已失效（数据文件被改名移走）
//	<path>.rebuild   存在表示该盘位是换入新盘、重建未完成
//	<path>.rebuilt   文本文件，每行一个已重建条带号
//
// 这些标记保证“同一写入序列与断电点”在重新打开后得到逐字节相同的内容。
type FileDisk struct {
	mu       sync.RWMutex
	index    int
	path     string
	stripes  int
	f        *os.File
	failed   bool
	building bool
	rebuilt  map[int]bool
}

// NewFileDisk 创建一块全新的成员盘，文件预分配为 stripes 个零块。
func NewFileDisk(index, stripes int, path string) (*FileDisk, error) {
	return openOrCreateFileDisk(index, stripes, path, true)
}

// NewReplacementFileDisk 在一个已失效（存在 .dead 标记）的盘位上创建全新
// 替换盘：删除 .dead 标记并建立零填充新数据文件。
func NewReplacementFileDisk(index, stripes int, path string) (*FileDisk, error) {
	d, err := OpenFileDisk(index, stripes, path)
	if err != nil {
		return nil, err
	}
	if err := d.ReplaceWithFresh(); err != nil {
		return nil, err
	}
	return d, nil
}

// OpenFileDisk 打开一块已有成员盘，依据标记文件识别失效/重建状态。
func OpenFileDisk(index, stripes int, path string) (*FileDisk, error) {
	return openOrCreateFileDisk(index, stripes, path, false)
}

func openOrCreateFileDisk(index, stripes int, path string, create bool) (*FileDisk, error) {
	d := &FileDisk{
		index:   index,
		path:    path,
		stripes: stripes,
		failed:  true,
		rebuilt: map[int]bool{},
	}
	if _, err := os.Stat(path + ".dead"); err == nil {
		return d, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	flag := os.O_RDWR
	if create {
		flag = os.O_RDWR | os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		return nil, err
	}
	if create {
		if err := f.Truncate(int64(stripes) * int64(BlockSize)); err != nil {
			f.Close()
			return nil, err
		}
	}
	d.f = f
	d.failed = false
	if _, err := os.Stat(path + ".rebuild"); err == nil {
		d.building = true
		if err := d.loadProgress(); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (d *FileDisk) loadProgress() error {
	f, err := os.Open(d.path + ".rebuilt")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil {
			return err
		}
		d.rebuilt[n] = true
	}
	return sc.Err()
}

// appendProgress 追加记录一个已重建条带并 fsync（顺序与重建顺序一致）。
func (d *FileDisk) appendProgress(stripe int) error {
	f, err := os.OpenFile(d.path+".rebuilt", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%d\n", stripe); err != nil {
		return err
	}
	return f.Sync()
}

func (d *FileDisk) Index() int { return d.index }

func (d *FileDisk) ReadBlock(stripe int, buf []byte) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.readLocked(stripe, buf)
}

// ReadBlockForRebuild：重建期间，已重建槽返回新值（立即参与读取），
// 未重建槽视为该盘仍缺这块而返回 ErrDiskDead。
func (d *FileDisk) ReadBlockForRebuild(stripe int, buf []byte) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.failed {
		return ErrDiskDead
	}
	if d.building && !d.rebuilt[stripe] {
		return ErrDiskDead
	}
	return d.readLocked(stripe, buf)
}

func (d *FileDisk) readLocked(stripe int, buf []byte) error {
	if d.failed {
		return ErrDiskDead
	}
	if stripe < 0 || stripe >= d.stripes || len(buf) != BlockSize {
		return ErrBlockOutOfRange
	}
	n, err := d.f.ReadAt(buf, int64(stripe)*int64(BlockSize))
	if err != nil {
		return err
	}
	if n != BlockSize {
		return ErrFormat
	}
	return nil
}

func (d *FileDisk) WriteBlock(stripe int, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failed {
		return ErrDiskDead
	}
	if stripe < 0 || stripe >= d.stripes || len(data) != BlockSize {
		return ErrBlockOutOfRange
	}
	if _, err := d.f.WriteAt(data, int64(stripe)*int64(BlockSize)); err != nil {
		return err
	}
	if err := d.f.Sync(); err != nil {
		return err
	}
	// 重建期间对新盘的正常写入同样修复该槽，记录进度保证崩溃后续建。
	if d.building && !d.rebuilt[stripe] {
		if err := d.appendProgress(stripe); err != nil {
			return err
		}
		d.rebuilt[stripe] = true
	}
	return nil
}

func (d *FileDisk) WriteBlockForRebuild(stripe int, data []byte) error {
	return d.WriteBlock(stripe, data)
}

func (d *FileDisk) MarkRebuilding() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failed {
		return ErrDiskDead
	}
	d.building = true
	f, err := os.OpenFile(d.path+".rebuild", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil && !os.IsExist(err) {
		return err
	}
	if f != nil {
		return f.Close()
	}
	return nil
}

func (d *FileDisk) Rebuilding() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.building
}

func (d *FileDisk) Rebuilt(stripe int) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return !d.failed && (!d.building || d.rebuilt[stripe])
}

func (d *FileDisk) FinishRebuild() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.building {
		return nil
	}
	if err := d.f.Sync(); err != nil {
		return err
	}
	d.building = false
	if err := os.Remove(d.path + ".rebuilt"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Remove(d.path + ".rebuild")
}

// Fail 模拟整盘失效：关闭并把数据文件改名为 .dead。之后任何 IO 失败。
func (d *FileDisk) Fail() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failed {
		return
	}
	d.failed = true
	d.building = false
	if d.f != nil {
		d.f.Close()
		d.f = nil
	}
	if _, err := os.Stat(d.path); err == nil {
		_ = os.Rename(d.path, d.path+".dead")
	}
}

func (d *FileDisk) Failed() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.failed
}

// ReplaceWithFresh 用一块全新零盘替换失效盘位：移除 .dead，创建空数据文件。
func (d *FileDisk) ReplaceWithFresh() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.failed {
		return ErrRebuildHealthyDisk
	}
	_ = os.Remove(d.path + ".dead")
	f, err := os.OpenFile(d.path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(d.stripes) * int64(BlockSize)); err != nil {
		f.Close()
		return err
	}
	d.f = f
	d.failed = false
	d.rebuilt = map[int]bool{}
	return nil
}

func (d *FileDisk) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f != nil {
		err := d.f.Close()
		d.f = nil
		return err
	}
	return nil
}
