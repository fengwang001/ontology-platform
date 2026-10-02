// Package compaction 实现分层压实输入选择器：按层得分挑选待压实层，
// 用轮转指针选起点文件，推出下一层重叠集合并尝试同层扩张。
// 所有方法在互斥锁保护下执行，并发调用等价于某个串行顺序，
// 相同调用序列重放得到完全相同的选择与指针。
package compaction

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
)

var (
	ErrTooFewLevels     = errors.New("compaction: L must be >= 2")
	ErrCapCountMismatch = errors.New("compaction: len(cap) must be L-1")
	ErrCapNonPositive   = errors.New("compaction: cap entries must be positive")
	ErrXNonPositive     = errors.New("compaction: X must be positive")

	ErrLevelOutOfRange = errors.New("compaction: file level out of range 1..L")
	ErrBadRange        = errors.New("compaction: file min key greater than max key")
	ErrNonPositiveSize = errors.New("compaction: file size must be positive")
	ErrDuplicateID     = errors.New("compaction: duplicate file id")
	ErrOverlap         = errors.New("compaction: file overlaps existing file in same level")

	ErrNothingToCompact = errors.New("compaction: nothing to do, max level score < 1")
)

type File struct {
	ID    uint64
	Level int
	Min   []byte
	Max   []byte
	Size  int64
}

type Picker struct {
	mu     sync.Mutex
	L      int
	X      int64
	caps   []int64
	byID   map[uint64]*File
	levels [][]*File
	ptr    [][]byte
}

func NewPicker(L int, caps []int64, X int64) (*Picker, error) {
	if L < 2 {
		return nil, fmt.Errorf("%w: got L=%d", ErrTooFewLevels, L)
	}
	if len(caps) != L-1 {
		return nil, fmt.Errorf("%w: got %d caps for L=%d", ErrCapCountMismatch, len(caps), L)
	}
	for i, c := range caps {
		if c <= 0 {
			return nil, fmt.Errorf("%w: cap for level %d is %d", ErrCapNonPositive, i+1, c)
		}
	}
	if X <= 0 {
		return nil, fmt.Errorf("%w: got X=%d", ErrXNonPositive, X)
	}
	p := &Picker{
		L:      L,
		X:      X,
		caps:   make([]int64, L+1),
		byID:   make(map[uint64]*File),
		levels: make([][]*File, L+1),
		ptr:    make([][]byte, L+1),
	}
	copy(p.caps[1:], caps)
	return p, nil
}

func (p *Picker) AddFile(f File) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if f.Level < 1 || f.Level > p.L {
		return fmt.Errorf("%w: got level %d (L=%d)", ErrLevelOutOfRange, f.Level, p.L)
	}
	if bytes.Compare(f.Min, f.Max) > 0 {
		return fmt.Errorf("%w: id=%d min=%q max=%q", ErrBadRange, f.ID, f.Min, f.Max)
	}
	if f.Size <= 0 {
		return fmt.Errorf("%w: id=%d size=%d", ErrNonPositiveSize, f.ID, f.Size)
	}
	if _, dup := p.byID[f.ID]; dup {
		return fmt.Errorf("%w: id=%d", ErrDuplicateID, f.ID)
	}
	for _, g := range p.levels[f.Level] {
		if rangesOverlap(g.Min, g.Max, f.Min, f.Max) {
			return fmt.Errorf("%w: id=%d [%q,%q] overlaps id=%d [%q,%q] in level %d",
				ErrOverlap, f.ID, f.Min, f.Max, g.ID, g.Min, g.Max, f.Level)
		}
	}

	stored := &File{
		ID:    f.ID,
		Level: f.Level,
		Min:   bytes.Clone(f.Min),
		Max:   bytes.Clone(f.Max),
		Size:  f.Size,
	}
	lv := p.levels[f.Level]
	pos := sort.Search(len(lv), func(i int) bool {
		return bytes.Compare(lv[i].Min, stored.Min) > 0
	})
	lv = append(lv, nil)
	copy(lv[pos+1:], lv[pos:])
	lv[pos] = stored
	p.levels[f.Level] = lv
	p.byID[stored.ID] = stored
	return nil
}

// rangesOverlap 判定闭区间 [amin,amax] 与 [bmin,bmax] 是否相交，端点相等算相交。
func rangesOverlap(amin, amax, bmin, bmax []byte) bool {
	return bytes.Compare(amin, bmax) <= 0 && bytes.Compare(bmin, amax) <= 0
}

func (p *Picker) Pick() (inputs []File, overlaps []File, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// ① 选层：得分 = 本层总字节 / cap[i]，交叉相乘比较，并列取层号小者。
	best := -1
	var bestBytes int64
	for i := 1; i <= p.L-1; i++ {
		var total int64
		for _, f := range p.levels[i] {
			total += f.Size
		}
		if best == -1 || crossGreater(total, p.caps[i], bestBytes, p.caps[best]) {
			best, bestBytes = i, total
		}
	}
	// 最大得分 < 1（bestBytes/cap < 1）则无事可做；恰等于 1 可压实。
	if bestBytes < p.caps[best] {
		return nil, nil, fmt.Errorf("%w: best level %d score %d/%d",
			ErrNothingToCompact, best, bestBytes, p.caps[best])
	}

	// ② 起点文件：第一个最小键严格大于 ptr 的文件；ptr 为「无」取第一个；
	// 没有这样的文件则回绕取第一个。
	lv := p.levels[best]
	startIdx := 0
	if p.ptr[best] != nil {
		startIdx = sort.Search(len(lv), func(i int) bool {
			return bytes.Compare(lv[i].Min, p.ptr[best]) > 0
		})
		if startIdx == len(lv) {
			startIdx = 0
		}
	}
	start := lv[startIdx]

	// ③ 下层重叠集 O。
	o := overlapping(p.levels[best+1], start.Min, start.Max)

	// ④ 扩张判定。
	rMin, rMax := start.Min, start.Max
	for _, f := range o {
		if bytes.Compare(f.Min, rMin) < 0 {
			rMin = f.Min
		}
		if bytes.Compare(f.Max, rMax) > 0 {
			rMax = f.Max
		}
	}
	t := overlapping(lv, rMin, rMax)
	selected := []*File{start}
	if len(t) > 1 {
		var sum int64
		for _, f := range t {
			sum += f.Size
		}
		for _, f := range o {
			sum += f.Size
		}
		if sum < p.X {
			tMin, tMax := t[0].Min, t[0].Max
			for _, f := range t[1:] {
				if bytes.Compare(f.Min, tMin) < 0 {
					tMin = f.Min
				}
				if bytes.Compare(f.Max, tMax) > 0 {
					tMax = f.Max
				}
			}
			o2 := overlapping(p.levels[best+1], tMin, tMax)
			if sameIDSet(o, o2) {
				selected = t
			}
		}
	}

	// ⑤ 推进指针：本层输入文件中最小键最大者的最小键。
	advance := selected[0].Min
	for _, f := range selected[1:] {
		if bytes.Compare(f.Min, advance) > 0 {
			advance = f.Min
		}
	}
	p.ptr[best] = bytes.Clone(advance)

	inputs = snapshot(selected)
	overlaps = snapshot(o)
	return inputs, overlaps, nil
}

func (p *Picker) Files(level int) ([]File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if level < 1 || level > p.L {
		return nil, fmt.Errorf("%w: got level %d (L=%d)", ErrLevelOutOfRange, level, p.L)
	}
	return snapshot(p.levels[level]), nil
}

func (p *Picker) Pointer(level int) (key []byte, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if level < 1 || level > p.L-1 || p.ptr[level] == nil {
		return nil, false
	}
	return bytes.Clone(p.ptr[level]), true
}

// crossGreater 报告 a/b 是否严格大于 c/d（b、 d 为正数），用交叉相乘避免浮点误差。
func crossGreater(a, b, c, d int64) bool {
	left := new(big.Int).Mul(big.NewInt(a), big.NewInt(d))
	right := new(big.Int).Mul(big.NewInt(c), big.NewInt(b))
	return left.Cmp(right) > 0
}

// overlapping 返回按 Min 升序的 lv 中与闭区间 [lo,hi] 相交的全部文件。
func overlapping(lv []*File, lo, hi []byte) []*File {
	var out []*File
	for _, f := range lv {
		if bytes.Compare(f.Min, hi) > 0 {
			break
		}
		if bytes.Compare(f.Max, lo) >= 0 {
			out = append(out, f)
		}
	}
	return out
}

// sameIDSet 报告两个按 Min 升序的文件切片是否编号集合相同。
func sameIDSet(a, b []*File) bool {
	if len(a) != len(b) {
		return false
	}
	ids := make(map[uint64]int, len(a))
	for _, f := range a {
		ids[f.ID]++
	}
	for _, f := range b {
		ids[f.ID]--
		if ids[f.ID] < 0 {
			return false
		}
	}
	return true
}

// snapshot 拷贝文件切片为值切片，保持原有（按 Min 升序）顺序。
func snapshot(files []*File) []File {
	out := make([]File, len(files))
	for i, f := range files {
		out[i] = File{
			ID:    f.ID,
			Level: f.Level,
			Min:   bytes.Clone(f.Min),
			Max:   bytes.Clone(f.Max),
			Size:  f.Size,
		}
	}
	return out
}
