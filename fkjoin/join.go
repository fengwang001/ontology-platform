package fkjoin

import (
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"sort"
	"sync"
)

// Row 是左表或右表中的一行。
type Row struct {
	FK    *string // 外键；nil 表示为空（仅左表使用）
	Value string  // 行值
}

// Entry 是内连接结果集中的一条结果。
type Entry struct {
	LeftID string
	Key    string
	Left   string
	Right  string
}

// Response 是右表对一次订阅的响应，按产生顺序进入 FIFO 队列，
// 投递时校验左行哈希，不一致则丢弃并计数。
type Response struct {
	Seq    uint64 // 产生顺序号（入队时分配，单调递增）
	LeftID string
	Key    string
	Value  string // 产生响应时右表该键的值
	Hash   uint64 // 产生响应时左行的哈希
}

// Delivery 描述一次 Deliver 的结果。
type Delivery struct {
	Resp     Response
	Applied  bool   // true 表示哈希校验通过并更新了结果
	Stale    bool   // true 表示哈希不一致被丢弃（丢弃数加一）
	Orphan   bool   // true 表示左行已不存在，结果按过期丢弃
	Evidence string // 判定依据（哈希对比、订阅与右表状态）
}

// HashRow 计算左行哈希：由左行标识、外键与左值决定。
// 外键为空时按 "\x00" 参与计算，与空字符串键区分。
func HashRow(id string, row Row) uint64 {
	h := fnv.New64a()
	io.WriteString(h, id)
	h.Write([]byte{0})
	if row.FK == nil {
		h.Write([]byte{0})
	} else {
		h.Write([]byte{1})
		io.WriteString(h, *row.FK)
	}
	h.Write([]byte{0})
	io.WriteString(h, row.Value)
	return h.Sum64()
}

// Joiner 维护左表、右表、订阅、FIFO 响应队列与内连接结果。
// 所有方法可并发调用；结果视图可并发读取且逐条一致。
type Joiner struct {
	mu        sync.RWMutex
	maxPend   int
	left      map[string]Row
	right     map[string]string
	subs      map[string]string // 左行 ID -> 订阅的外键
	queue     []Response        // 待投递响应，FIFO
	results   map[string]Entry  // 左行 ID -> 结果条目
	discarded uint64            // 哈希校验失败被丢弃的响应数
	seq       uint64            // 响应顺序号
	logSeq    uint64            // 日志顺序号
	logger    *log.Logger
}

// New 创建 Joiner。maxPending 为待投递响应上限，必须为正数。
func New(maxPending int, logW io.Writer) *Joiner {
	if maxPending <= 0 {
		panic("fkjoin: maxPending must be positive")
	}
	if logW == nil {
		logW = io.Discard
	}
	return &Joiner{
		maxPend: maxPending,
		left:    make(map[string]Row),
		right:   make(map[string]string),
		subs:    make(map[string]string),
		results: make(map[string]Entry),
		logger:  log.New(logW, "", 0),
	}
}

// logf 追加一条带单调序号的日志，调用时必须持有写锁，
// 以保证日志顺序与状态变更顺序一致（确定性输出）。
func (j *Joiner) logf(format string, args ...any) {
	j.logSeq++
	args = append([]any{j.logSeq}, args...)
	j.logger.Printf("#%04d "+format, args...)
}

// UpsertLeft 插入或更新左表行。
// 外键非空时建立订阅；外键为空时撤销订阅并移出结果。
// 外键非空但右表尚无该键时，订阅保留、结果暂缺，
// 待右表出现该键且响应投递后进入结果。
// 外键为空字符串属于非法输入，拒绝且不产生任何副作用。
func (j *Joiner) UpsertLeft(id string, row Row) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if row.FK != nil && *row.FK == "" {
		j.logf("REJECT op=UpsertLeft left=%s reason=%q", id, ErrEmptyKey)
		return reject("UpsertLeft", ErrEmptyKey)
	}
	old, existed := j.left[id]
	fkChanged := existed && old.FK != nil && (row.FK == nil || *old.FK != *row.FK)
	if fkChanged {
		delete(j.subs, id)
		delete(j.results, id)
	}
	j.left[id] = row
	if row.FK != nil {
		j.subs[id] = *row.FK
	}
	if existed && !fkChanged && old.FK != nil && old.Value != row.Value {
		if e, ok := j.results[id]; ok {
			e.Left = row.Value
			j.results[id] = e
		}
	}
	j.logf("UPSERT-LEFT left=%s fk=%s value=%q hash=%d sub=%t result=%t",
		id, fkString(row.FK), row.Value, HashRow(id, row), row.FK != nil, !fkChanged && row.FK != nil)
	return nil
}

// DeleteLeft 删除左表行，撤销订阅并撤回结果。
func (j *Joiner) DeleteLeft(id string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	row, ok := j.left[id]
	if !ok {
		j.logf("DELETE-LEFT left=%s noop reason=left-absent", id)
		return
	}
	delete(j.left, id)
	delete(j.subs, id)
	_, hadResult := j.results[id]
	delete(j.results, id)
	j.logf("DELETE-LEFT left=%s fk=%s withdrawn-result=%t", id, fkString(row.FK), hadResult)
}

// UpsertRight 插入或更新右表行。键为空字符串时拒绝。
// 已存在且引用该键的结果会同步刷新右值；订阅该键但尚无结果的
// 左行需等待右表侧产生响应并投递后才进入结果。
func (j *Joiner) UpsertRight(key, value string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if key == "" {
		j.logf("REJECT op=UpsertRight reason=%q", ErrEmptyKey)
		return reject("UpsertRight", ErrEmptyKey)
	}
	j.right[key] = value
	refreshed := 0
	for id, e := range j.results {
		if e.Key == key {
			e.Right = value
			j.results[id] = e
			refreshed++
		}
	}
	j.logf("UPSERT-RIGHT key=%s value=%q refreshed-results=%d", key, value, refreshed)
	return nil
}

// DeleteRight 删除右表键，撤回所有引用该键的结果。
// 订阅保留：右表键重新出现并经响应确认后结果可恢复。
func (j *Joiner) DeleteRight(key string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if key == "" {
		j.logf("REJECT op=DeleteRight reason=%q", ErrEmptyKey)
		return reject("DeleteRight", ErrEmptyKey)
	}
	if _, ok := j.right[key]; !ok {
		j.logf("DELETE-RIGHT key=%s noop reason=right-absent", key)
		return nil
	}
	delete(j.right, key)
	var withdrawn []string
	for id, e := range j.results {
		if e.Key == key {
			withdrawn = append(withdrawn, id)
		}
	}
	sort.Strings(withdrawn)
	for _, id := range withdrawn {
		delete(j.results, id)
	}
	j.logf("DELETE-RIGHT key=%s withdrawn-results=%v", key, withdrawn)
	return nil
}

// Lookup 供右表侧驱动调用：返回订阅了 key 的左行及其当前哈希。
// 右表侧据此为每个订阅者产生一条响应并调用 Enqueue。
func (j *Joiner) Lookup(key string) map[string]uint64 {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make(map[string]uint64)
	for id, k := range j.subs {
		if k == key {
			out[id] = HashRow(id, j.left[id])
		}
	}
	return out
}

// Enqueue 将右表侧产生的响应追加到 FIFO 队列尾部。
// 键为空或待投递响应达到上限时拒绝，且不产生任何副作用。
func (j *Joiner) Enqueue(leftID, key, value string, hash uint64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if key == "" {
		j.logf("REJECT op=Enqueue left=%s reason=%q", leftID, ErrEmptyKey)
		return reject("Enqueue", ErrEmptyKey)
	}
	if len(j.queue) >= j.maxPend {
		j.logf("REJECT op=Enqueue left=%s key=%s reason=%q pending=%d limit=%d",
			leftID, key, ErrPendingLimit, len(j.queue), j.maxPend)
		return reject("Enqueue", ErrPendingLimit)
	}
	j.seq++
	j.queue = append(j.queue, Response{Seq: j.seq, LeftID: leftID, Key: key, Value: value, Hash: hash})
	j.logf("ENQUEUE seq=%d left=%s key=%s value=%q hash=%d pending=%d",
		j.seq, leftID, key, value, hash, len(j.queue))
	return nil
}

// Deliver 投递队首响应，按序校验：
//  1. 左行存在；
//  2. 左行哈希与响应一致（左行未变更）；
//  3. 右表当前仍持有响应产生时的键值（右表未变更）。
//
// 全部通过则更新结果；任一失败则按过期响应丢弃并计数。
// 队列为空时拒绝。
func (j *Joiner) Deliver() (Delivery, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.queue) == 0 {
		j.logf("REJECT op=Deliver reason=%q", ErrEmptyQueue)
		return Delivery{}, reject("Deliver", ErrEmptyQueue)
	}
	resp := j.queue[0]
	j.queue = j.queue[1:]
	d := Delivery{Resp: resp}
	row, ok := j.left[resp.LeftID]
	rv, rok := j.right[resp.Key]
	switch {
	case !ok:
		d.Orphan = true
		d.Stale = true
		j.discarded++
		d.Evidence = fmt.Sprintf("left=%s absent; response expired", resp.LeftID)
	case HashRow(resp.LeftID, row) != resp.Hash:
		d.Stale = true
		j.discarded++
		d.Evidence = fmt.Sprintf("hash mismatch: current=%d response=%d", HashRow(resp.LeftID, row), resp.Hash)
	case !rok || rv != resp.Value:
		d.Stale = true
		j.discarded++
		if rok {
			d.Evidence = fmt.Sprintf("right value changed: current=%q response=%q", rv, resp.Value)
		} else {
			d.Evidence = fmt.Sprintf("right key %s absent; response expired", resp.Key)
		}
	default:
		d.Applied = true
		j.results[resp.LeftID] = Entry{LeftID: resp.LeftID, Key: resp.Key, Left: row.Value, Right: resp.Value}
		d.Evidence = fmt.Sprintf("hash match=%d; right[%s]=%q at enqueue; sub=%q",
			resp.Hash, resp.Key, resp.Value, j.subs[resp.LeftID])
	}
	j.logf("DELIVER seq=%d left=%s key=%s applied=%t stale=%t discarded=%d evidence=%s",
		resp.Seq, resp.LeftID, resp.Key, d.Applied, d.Stale, j.discarded, d.Evidence)
	return d, nil
}

// Drain 排空全部待投递响应，返回各次投递结果。
// 空队列时返回空切片（不属于拒绝场景）。
func (j *Joiner) Drain() []Delivery {
	var out []Delivery
	for j.Pending() > 0 {
		d, err := j.Deliver()
		if err != nil {
			return out
		}
		out = append(out, d)
	}
	return out
}

// Results 返回按 LeftID 排序的结果快照，可并发读取。
func (j *Joiner) Results() []Entry {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make([]Entry, 0, len(j.results))
	for _, e := range j.results {
		out = append(out, e)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].LeftID < out[b].LeftID })
	return out
}

// Discarded 返回被丢弃的响应总数。
func (j *Joiner) Discarded() uint64 {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.discarded
}

// Pending 返回待投递响应数量。
func (j *Joiner) Pending() int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return len(j.queue)
}

// Subscriptions 返回订阅快照（左行 ID -> 外键）。
func (j *Joiner) Subscriptions() map[string]string {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make(map[string]string, len(j.subs))
	for id, k := range j.subs {
		out[id] = k
	}
	return out
}

// Left 返回左表快照。
func (j *Joiner) Left() map[string]Row {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make(map[string]Row, len(j.left))
	for id, row := range j.left {
		out[id] = row
	}
	return out
}

// Right 返回右表快照。
func (j *Joiner) Right() map[string]string {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make(map[string]string, len(j.right))
	for k, v := range j.right {
		out[k] = v
	}
	return out
}

// RightValue 返回右表某个键的当前值，供右表侧驱动产生响应时使用。
func (j *Joiner) RightValue(key string) (string, bool) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	v, ok := j.right[key]
	return v, ok
}

// NaiveJoin 对给定的左右表快照计算朴素内连接，作为判定基准。
func NaiveJoin(left map[string]Row, right map[string]string) []Entry {
	out := make([]Entry, 0, len(left))
	for id, row := range left {
		if row.FK == nil {
			continue
		}
		rv, ok := right[*row.FK]
		if !ok {
			continue
		}
		out = append(out, Entry{LeftID: id, Key: *row.FK, Left: row.Value, Right: rv})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].LeftID < out[b].LeftID })
	return out
}

// fkString 返回外键的日志表示，空外键显示为 <nil>。
func fkString(fk *string) string {
	if fk == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", *fk)
}
