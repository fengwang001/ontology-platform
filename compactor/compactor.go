// Package compactor 实现带多快照可见性的压实迭代器。
//
// 运行集由按序号递增的记录（键、序号、种类、值）组成，支持 Put/Merge/Delete
// 三种写入、快照持有与释放、按序号读取，以及一次性重写运行集的 Compact。
// Compact 按存活快照把同一键的多个版本分条带折叠：丢弃被遮蔽的旧版本、
// 折叠合并操作数、在满足条件时清除墓碑并把最旧 Merge 转为 Put，保证压实前后
// 任一存活快照与最新视图的读取结果逐一相同。
package compactor

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// Latest 表示最新视图，可作为 Get 的快照参数。
const Latest = math.MaxUint64

// maxAbsValue 是写入值与合并操作数允许的最大绝对值（10^12）。
const maxAbsValue = int64(1_000_000_000_000)

var (
	// ErrEmptyKey 表示键为空字符串。
	ErrEmptyKey = errors.New("compactor: empty key")
	// ErrRange 表示值或操作数绝对值超过 10^12。
	ErrRange = errors.New("compactor: value out of range")
	// ErrNotHeld 表示快照序号未被持有（Release 或 Get 使用）。
	ErrNotHeld = errors.New("compactor: snapshot not held")
)

// Kind 是记录种类。
type Kind int

const (
	KindPut Kind = iota
	KindMerge
	KindDelete
)

func (k Kind) String() string {
	switch k {
	case KindPut:
		return "Put"
	case KindMerge:
		return "Merge"
	case KindDelete:
		return "Delete"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Record 是运行集中的一条记录。
type Record struct {
	Key   string
	Seq   uint64
	Kind  Kind
	Value int64
}

func (r Record) String() string {
	return fmt.Sprintf("%s(%s,%d)@%d", r.Kind, r.Key, r.Value, r.Seq)
}

// Stats 是 Compact 的账目，满足 In = Out + Shadowed + Folded + TombDropped。
type Stats struct {
	In          int64 // 压实前运行集记录数
	Out         int64 // 压实后运行集记录数
	Shadowed    int64 // 被条带内更新记录遮蔽而丢弃的记录数
	Folded      int64 // 被折叠进单条产出的记录数（不含产出本身）
	TombDropped int64 // 被清除的墓碑数
	MergeToPut  int64 // 最旧 Merge 转 Put 的次数（不改变记录数）
}

func (s Stats) String() string {
	return fmt.Sprintf("Stats{In:%d Out:%d Shadowed:%d Folded:%d TombDropped:%d MergeToPut:%d}",
		s.In, s.Out, s.Shadowed, s.Folded, s.TombDropped, s.MergeToPut)
}

// Compactor 是带多快照可见性的压实存储。所有方法可并发调用，
// 结果等价于某个串行顺序；Compact 是一个原子步骤。
type Compactor struct {
	mu      sync.Mutex
	deeper  map[string]int64 // 更深层已存在的键及基值，复制保存、此后不变
	records []Record         // 运行集，按序号升序
	holds   map[uint64]int   // 快照序号 -> 持有次数
	seq     uint64           // 已分配的最大序号

	// stripeProbes 统计上一次 Compact 中条带二分定位的比较次数，
	// 不得超过 In*(floor(log2(|S|+1))+1)。
	stripeProbes int64
}

// New 创建一个压实器。deeper 表示更深层已存在的键及其基值（可为 0，
// 存在即算有），会被复制保存，此后不变。
func New(deeper map[string]int64) *Compactor {
	d := make(map[string]int64, len(deeper))
	for k, v := range deeper {
		d[k] = v
	}
	return &Compactor{
		deeper: d,
		holds:  make(map[uint64]int),
	}
}

// Put 追加一条 Put 记录并返回序号。
func (c *Compactor) Put(key string, value int64) (uint64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	if value > maxAbsValue || value < -maxAbsValue {
		return 0, ErrRange
	}
	return c.append(key, KindPut, value), nil
}

// Merge 追加一条 Merge 记录（语义为加 delta）并返回序号。
func (c *Compactor) Merge(key string, delta int64) (uint64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	if delta > maxAbsValue || delta < -maxAbsValue {
		return 0, ErrRange
	}
	return c.append(key, KindMerge, delta), nil
}

// Delete 追加一条 Delete 记录并返回序号。
func (c *Compactor) Delete(key string) (uint64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	return c.append(key, KindDelete, 0), nil
}

func (c *Compactor) append(key string, kind Kind, value int64) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	c.records = append(c.records, Record{Key: key, Seq: c.seq, Kind: kind, Value: value})
	return c.seq
}

// Snapshot 返回当前最大序号（尚无写入为 0）并记一次持有。
// 同一序号可被多次持有，按次数计。
func (c *Compactor) Snapshot() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.holds[c.seq]++
	return c.seq
}

// Release 取消一次对序号 s 的持有；s 未被持有时返回 ErrNotHeld。
func (c *Compactor) Release(s uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.holds[s] <= 0 {
		return ErrNotHeld
	}
	c.holds[s]--
	if c.holds[s] == 0 {
		delete(c.holds, s)
	}
	return nil
}

// Records 返回运行集按序号升序的记录副本。
func (c *Compactor) Records() []Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Record(nil), c.records...)
}

// Get 在序号不大于 s 的记录中读取键 k。s 必须是被持有的序号或 Latest。
// 按序号从新到旧扫描：遇 Put 累计其值并停止（存在）；遇 Delete 停止
// （存在当且仅当此前累计过 Merge）；遇 Merge 累计并继续；到最旧仍未遇
// Put/Delete 时，存在当且仅当累计过 Merge 或 deeper 含 k，并再加上
// deeper[k]（无则不加）。不存在返回 (0, false, nil)。
func (c *Compactor) Get(key string, s uint64) (int64, bool, error) {
	if key == "" {
		return 0, false, ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s != Latest && c.holds[s] <= 0 {
		return 0, false, ErrNotHeld
	}
	var sum int64
	merged := false
	for i := len(c.records) - 1; i >= 0; i-- {
		r := c.records[i]
		if r.Key != key || r.Seq > s {
			continue
		}
		switch r.Kind {
		case KindPut:
			return sum + r.Value, true, nil
		case KindDelete:
			return sum, merged, nil
		case KindMerge:
			sum += r.Value
			merged = true
		}
	}
	base, ok := c.deeper[key]
	if !merged && !ok {
		return 0, false, nil
	}
	if ok {
		sum += base
	}
	return sum, true, nil
}

// stripeOf 用二分查找定位序号 q 所属条带：S∪{Latest} 中不小于 q 的最小元素。
// snaps 为存活快照序号的升序去重切片。每次迭代计一次 stripeProbes。
func (c *Compactor) stripeOf(q uint64, snaps []uint64) uint64 {
	lo, hi := 0, len(snaps)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		c.stripeProbes++
		if snaps[mid] < q {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(snaps) {
		return snaps[lo]
	}
	return Latest
}

// Compact 一次性重写运行集，各键独立处理，是一个原子步骤。
// 返回的 Stats 满足 In = Out + Shadowed + Folded + TombDropped。
func (c *Compactor) Compact() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()

	var stats Stats
	c.stripeProbes = 0
	stats.In = int64(len(c.records))

	snaps := make([]uint64, 0, len(c.holds))
	for s := range c.holds {
		snaps = append(snaps, s)
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i] < snaps[j] })

	byKey := make(map[string][]Record)
	keys := make([]string, 0)
	for _, r := range c.records {
		if _, ok := byKey[r.Key]; !ok {
			keys = append(keys, r.Key)
		}
		byKey[r.Key] = append(byKey[r.Key], r)
	}
	sort.Strings(keys)

	out := make([]Record, 0, len(c.records))
	for _, key := range keys {
		recs := byKey[key] // 序号升序
		stripes := make([]uint64, len(recs))
		for i, r := range recs {
			stripes[i] = c.stripeOf(r.Seq, snaps)
		}
		_, hasBase := c.deeper[key]

		// 按条带分组（同一条带的记录在序号上连续），从新到旧逐条带折叠。
		keyOut := make([]Record, 0, len(recs)) // 新到旧
		for hi := len(recs); hi > 0; {
			lo := hi - 1
			stripe := stripes[lo]
			for lo > 0 && stripes[lo-1] == stripe {
				lo--
			}
			keyOut = append(keyOut, foldStripe(recs[lo:hi], &stats))
			hi = lo
		}

		// 收尾：反复清除最旧的 Delete（deeper 不含该键时）。
		for n := len(keyOut); n > 0 && keyOut[n-1].Kind == KindDelete && !hasBase; n = len(keyOut) {
			keyOut = keyOut[:n-1]
			stats.TombDropped++
		}
		// 收尾：最旧一条是 Merge 且 deeper 不含该键时改为同序号 Put。
		if n := len(keyOut); n > 0 && keyOut[n-1].Kind == KindMerge && !hasBase {
			keyOut[n-1].Kind = KindPut
			stats.MergeToPut++
		}
		out = append(out, keyOut...)
	}

	// 产出序号取自原记录且互不重复，按序号升序排列即得确定性的新运行集。
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	c.records = out
	stats.Out = int64(len(out))
	return stats
}

// foldStripe 折叠同一键同一条带内的记录（group 按序号升序），产出一条记录。
// e1 为最新一条：e1 是 Put/Delete 则原样产出，其余计入 Shadowed；e1 是
// Merge 则向下累加连续 Merge，直到条带内第一条非 Merge 记录 b 或条带耗尽：
// b 为 Put 产出 Put(累计和+b 的值)，b 为 Delete 产出 Put(累计和)，无 b 产出
// Merge(累计和)；产出序号取 e1 的序号；被并入的 j 条（含 b）计 j-1 条
// Folded，b 之后条带内剩余的计入 Shadowed。
func foldStripe(group []Record, stats *Stats) Record {
	e1 := group[len(group)-1]
	if e1.Kind == KindPut || e1.Kind == KindDelete {
		stats.Shadowed += int64(len(group) - 1)
		return e1
	}
	var sum int64
	i := len(group) - 1
	for i >= 0 && group[i].Kind == KindMerge {
		sum += group[i].Value
		i--
	}
	merges := len(group) - 1 - i
	if i < 0 {
		// 条带耗尽，无基值记录，留作 Merge。
		stats.Folded += int64(merges - 1)
		return Record{Key: e1.Key, Seq: e1.Seq, Kind: KindMerge, Value: sum}
	}
	b := group[i]
	stats.Folded += int64(merges) // j = merges+1（含 b），计 j-1 条 Folded
	stats.Shadowed += int64(i)    // b 之后（更旧）的 i 条计入 Shadowed
	if b.Kind == KindPut {
		return Record{Key: e1.Key, Seq: e1.Seq, Kind: KindPut, Value: sum + b.Value}
	}
	return Record{Key: e1.Key, Seq: e1.Seq, Kind: KindPut, Value: sum}
}
