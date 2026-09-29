// Package ranking 提供按分数降序的增量排名维护器。
//
// 排序键：先分数降序，分数相同再按唯一标识升序。
//   - 位次（Position）：元素在全序中的从一起的位次，每个元素互不相同。
//   - 排名（Rank）：严格高于该元素的元素个数加一，同分共享同一排名并产生空洞。
//   - 稠密排名（DenseRank）：严格高于该元素的互异分数个数加一，同分共享且无空洞。
package ranking

import (
	"errors"
	"sync"
)

// 三类可判定的非法输入错误，互不相同的哨兵值，可用 errors.Is 判定。
var (
	ErrEmptyID     = errors.New("ranking: 标识为空")
	ErrDuplicateID = errors.New("ranking: 标识已存在")
	ErrIDNotFound  = errors.New("ranking: 标识不存在")
)

// Triple 是某个元素的三元组：位次、排名、稠密排名，均从一开始。
type Triple struct {
	Position  int
	Rank      int
	DenseRank int
}

type element struct {
	id    string
	score int64
}

// Leaderboard 增量维护所有元素的位次、排名与稠密排名，可并发使用。
type Leaderboard struct {
	mu         sync.RWMutex
	elems      []element      // 按 (分数降序, 标识升序) 排序
	index      map[string]int // 标识 -> elems 下标
	scoreCount map[int64]int  // 分数 -> 该分数上的元素个数
}

// New 返回一个空的 Leaderboard。
func New() *Leaderboard {
	return &Leaderboard{
		index:      make(map[string]int),
		scoreCount: make(map[int64]int),
	}
}

// less 报告下标 i 处的元素在全序中是否排在前 j 之前：先分数降序，再标识升序。
func (l *Leaderboard) less(i, j int) bool {
	if l.elems[i].score != l.elems[j].score {
		return l.elems[i].score > l.elems[j].score
	}
	return l.elems[i].id < l.elems[j].id
}

// search 返回 e 在全序中的插入位次（下标），即第一个不小于 e 的位置。
func (l *Leaderboard) search(e element) int {
	lo, hi := 0, len(l.elems)
	for lo < hi {
		mid := (lo + hi) / 2
		if l.elems[mid].score > e.score ||
			(l.elems[mid].score == e.score && l.elems[mid].id < e.id) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Insert 插入一个元素。标识为空返回 ErrEmptyID，标识已存在返回
// ErrDuplicateID；失败时不改变任何已有元素的三元组。
//
// 插入后所有分数严格更低的元素位次与排名加一；稠密排名仅当新元素
// 引入了一个新的更高分数取值时才加一。
func (l *Leaderboard) Insert(id string, score int64) error {
	if id == "" {
		return ErrEmptyID
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.index[id]; ok {
		return ErrDuplicateID
	}
	e := element{id: id, score: score}
	pos := l.search(e)
	l.elems = append(l.elems, element{})
	copy(l.elems[pos+1:], l.elems[pos:])
	l.elems[pos] = e
	for i := pos; i < len(l.elems); i++ {
		l.index[l.elems[i].id] = i
	}
	l.scoreCount[score]++
	return nil
}

// Delete 删除一个元素。标识为空返回 ErrEmptyID，标识不存在返回
// ErrIDNotFound；失败时不改变任何已有元素的三元组。
//
// 删除后所有分数严格更低的元素位次与排名减一；稠密排名仅当被删
// 元素是其分数上的最后一个元素时才减一。
func (l *Leaderboard) Delete(id string) error {
	if id == "" {
		return ErrEmptyID
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	pos, ok := l.index[id]
	if !ok {
		return ErrIDNotFound
	}
	score := l.elems[pos].score
	copy(l.elems[pos:], l.elems[pos+1:])
	l.elems = l.elems[:len(l.elems)-1]
	delete(l.index, id)
	for i := pos; i < len(l.elems); i++ {
		l.index[l.elems[i].id] = i
	}
	l.scoreCount[score]--
	if l.scoreCount[score] == 0 {
		delete(l.scoreCount, score)
	}
	return nil
}

// Get 返回标识对应元素的三元组；标识不存在时 ok 为 false。可并发调用。
func (l *Leaderboard) Get(id string) (t Triple, ok bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	pos, ok := l.index[id]
	if !ok {
		return Triple{}, false
	}
	score := l.elems[pos].score
	t.Position = pos + 1
	// 排名：严格更高分数的元素个数加一，即同分段第一个元素的位次。
	t.Rank = l.search(element{score: score, id: ""}) + 1
	// 稠密排名：严格更高的互异分数个数加一。
	t.DenseRank = 1
	for s := range l.scoreCount {
		if s > score {
			t.DenseRank++
		}
	}
	return t, true
}

// Len 返回当前元素个数。可并发调用。
func (l *Leaderboard) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.elems)
}

// SelfCheck 校验内部不变量，发现不一致时返回非 nil 错误。可并发调用。
//
// 校验内容：全序有序、index 与 elems 互逆、位次构成 1..n 的双射、
// scoreCount 与实际一致、每个元素的三元组满足定义。
func (l *Leaderboard) SelfCheck() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	n := len(l.elems)
	if len(l.index) != n {
		return errors.New("ranking: index 与 elems 大小不一致")
	}
	seen := make(map[string]bool, n)
	counts := make(map[int64]int)
	for i, e := range l.elems {
		if i > 0 && l.less(i, i-1) {
			return errors.New("ranking: 元素未按全序排列")
		}
		if seen[e.id] {
			return errors.New("ranking: 标识重复")
		}
		seen[e.id] = true
		if l.index[e.id] != i {
			return errors.New("ranking: index 与 elems 不互逆")
		}
		counts[e.score]++
		// 三元组定义校验：位次从一起且互异（由双射保证），
		// 排名 = 严格更高元素个数 + 1，稠密排名 = 严格更高互异分数个数 + 1。
		rank, dense := 1, 1
		for j := 0; j < n; j++ {
			if l.elems[j].score > e.score {
				rank++
			}
		}
		distinct := make(map[int64]bool)
		for j := 0; j < n; j++ {
			if l.elems[j].score > e.score {
				distinct[l.elems[j].score] = true
			}
		}
		dense += len(distinct)
		if rank != l.search(element{score: e.score, id: ""})+1 {
			return errors.New("ranking: 排名与定义不符")
		}
		d := 1
		for s := range l.scoreCount {
			if s > e.score {
				d++
			}
		}
		if dense != d {
			return errors.New("ranking: 稠密排名与定义不符")
		}
	}
	if len(counts) != len(l.scoreCount) {
		return errors.New("ranking: scoreCount 与实际分数取值不一致")
	}
	for s, c := range counts {
		if l.scoreCount[s] != c {
			return errors.New("ranking: scoreCount 计数不一致")
		}
	}
	return nil
}
