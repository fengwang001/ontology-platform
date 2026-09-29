package raid5

import "sync"

// memDisk 是内存成员盘，语义与 FileDisk 一致（用于高速并发测试）。
type memDisk struct {
	mu       sync.RWMutex
	index    int
	stripes  int
	data     [][]byte
	failed   bool
	building bool
	rebuilt  map[int]bool
}

func newMemDiskSet(n, stripes int) []Disk {
	disks := make([]Disk, n)
	for i := 0; i < n; i++ {
		d := &memDisk{index: i, stripes: stripes, rebuilt: map[int]bool{}}
		d.data = make([][]byte, stripes)
		for s := range d.data {
			d.data[s] = make([]byte, BlockSize)
		}
		disks[i] = d
	}
	return disks
}

func (d *memDisk) Index() int { return d.index }

func (d *memDisk) ReadBlock(stripe int, buf []byte) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.failed {
		return ErrDiskDead
	}
	copy(buf, d.data[stripe])
	return nil
}

func (d *memDisk) ReadBlockForRebuild(stripe int, buf []byte) error {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.failed || (d.building && !d.rebuilt[stripe]) {
		return ErrDiskDead
	}
	copy(buf, d.data[stripe])
	return nil
}

func (d *memDisk) WriteBlock(stripe int, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failed {
		return ErrDiskDead
	}
	copy(d.data[stripe], data)
	if d.building {
		d.rebuilt[stripe] = true
	}
	return nil
}

func (d *memDisk) WriteBlockForRebuild(stripe int, data []byte) error {
	return d.WriteBlock(stripe, data)
}

func (d *memDisk) MarkRebuilding() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failed {
		return ErrDiskDead
	}
	d.building = true
	return nil
}

func (d *memDisk) Rebuilding() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.building
}
func (d *memDisk) Rebuilt(s int) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return !d.failed && (!d.building || d.rebuilt[s])
}

func (d *memDisk) FinishRebuild() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.building = false
	return nil
}

func (d *memDisk) Fail() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.failed {
		d.failed = true
		d.building = false
	}
}

func (d *memDisk) Failed() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.failed
}
func (d *memDisk) Close() error { return nil }

// cloneDisks 深拷贝一组内存盘，用于断电点前后逐字节对比。
func cloneDisks(src []Disk) []Disk {
	out := make([]Disk, len(src))
	for i, d := range src {
		m := d.(*memDisk)
		m.mu.RLock()
		c := &memDisk{
			index:    m.index,
			stripes:  m.stripes,
			failed:   m.failed,
			building: m.building,
			rebuilt:  map[int]bool{},
		}
		c.data = make([][]byte, len(m.data))
		for s := range m.data {
			c.data[s] = append([]byte(nil), m.data[s]...)
			if m.rebuilt[s] {
				c.rebuilt[s] = true
			}
		}
		m.mu.RUnlock()
		out[i] = c
	}
	return out
}
