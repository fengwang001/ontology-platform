// Package meter 维护每个主体在当前月号的解锁账。
package meter

import (
	"bytes"
	"errors"
	"sort"
)

// ErrInvalidArgument 表示构造或参数非法。
var ErrInvalidArgument = errors.New("meter: invalid argument")

// Kind 是解锁类别。
type Kind int

const (
	// Quota 表示占用每月免费额度的解锁。
	Quota Kind = iota
	// Gift 表示由礼赠令牌放行的解锁，不占额度。
	Gift
)

// Entry 是一条解锁记录。
type Entry struct {
	Article []byte
	FirstAt int64
	Kind    Kind
}

type book struct {
	month int64
	recs  map[string]Entry // 键为文章字节串（入存时已拷贝）
	used  int
}

// Meter 是所有主体当前月账的存储。
//
// 每个主体只保留“当前月号”一本账（账是 map，按值挂在 books 中），
// 月号变化即整体换一本新账，历史月不可见。登录合并在 map 上构造并集后
// 用一个全新 map 整体替换用户账，从不就地修改既有 map，因此不可能与设备
// 匿名账或任何历史结果共享底层存储。
type Meter struct {
	n       int64
	m       int64
	touched int
	books   map[string]book
}

// New 创建计量存储：n 为每月免费篇数，m 为月长秒数。
func New(n, m int64) (*Meter, error) {
	if n < 0 || n > 1000 || m < 1 || m > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	return &Meter{n: n, m: m, books: make(map[string]book)}, nil
}

// Month 返回 now 对应的月号。
func (x *Meter) Month(now int64) int64 {
	if now < 0 {
		return -1
	}
	return now / x.m
}

func keyOf(id []byte) string { return string(id) }

func cloneBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

// bookFor 返回主体在 now 所在月的账；月号变化即换新账。
// 注意：返回的是指针，调用方只能修改通过它做“整体替换”语义的字段，
// 常规读写仅操作 recs 这个 map；换月时 recs 指向新 map。
func (x *Meter) bookFor(subject []byte, now int64) *book {
	month := x.Month(now)
	key := keyOf(subject)
	bk, ok := x.books[key]
	if !ok || bk.month != month {
		// 历史月不可见：回查更早 now 得到的也是一张空账，且不会推进月份。
		if ok && month < bk.month {
			return &book{month: month, recs: map[string]Entry{}}
		}
		bk = book{month: month, recs: make(map[string]Entry)}
		x.books[key] = bk
	}
	// 取地址安全：books 中该条目此后只会被整体替换（map 元素不可取址，
	// 但这里 bk 是局部副本；写回通过 store 完成）。
	return &bk
}

// store 把主体当前月账写回（整体替换语义，保证 map 归该主体独占）。
func (x *Meter) store(subject []byte, bk book) {
	x.books[keyOf(subject)] = bk
}

// Lookup 查询主体本月账中是否已有该文章，触碰 1 条记录。
func (x *Meter) Lookup(subject, article []byte, now int64) (Entry, bool) {
	bk := x.bookFor(subject, now)
	x.touched++
	if e, ok := bk.recs[keyOf(article)]; ok {
		e.Article = cloneBytes(e.Article)
		return e, true
	}
	return Entry{}, false
}

// Used 返回主体本月已用额度篇数。
func (x *Meter) Used(subject []byte, now int64) int {
	return x.bookFor(subject, now).used
}

// RedeemQuota 写入一条额度类解锁。
func (x *Meter) RedeemQuota(subject, article []byte, now int64) {
	x.redeem(subject, article, now, Quota)
}

// RedeemGift 写入一条礼赠类解锁。
func (x *Meter) RedeemGift(subject, article []byte, now int64) {
	x.redeem(subject, article, now, Gift)
}

func (x *Meter) redeem(subject, article []byte, now int64, kind Kind) {
	bk := x.bookFor(subject, now)
	k := keyOf(article)
	x.touched++
	if _, ok := bk.recs[k]; ok {
		return
	}
	// 复制出一个新 map 再写，避免与合并结果等历史持有者共享底层 map。
	next := make(map[string]Entry, len(bk.recs)+1)
	for ak, e := range bk.recs {
		next[ak] = e
	}
	next[k] = Entry{Article: cloneBytes(article), FirstAt: now, Kind: kind}
	bk.recs = next
	if kind == Quota {
		bk.used++
	}
	x.store(subject, *bk)
}

// MergeInto 把 device 本月账并入 user 本月账：礼赠全保留，额度按
// (首次时刻, 文章字节序) 保留最早 n 篇。device 账保持原样。
func (x *Meter) MergeInto(user, device []byte, now int64) {
	if bytes.Equal(user, device) {
		return
	}
	ub := x.bookFor(user, now)
	db := x.bookFor(device, now)

	// union 是全新 map，绝不写回或修改用户/设备原有 map。
	union := make(map[string]Entry, len(ub.recs)+len(db.recs))
	quota := make([]Entry, 0, len(ub.recs)+len(db.recs))

	add := func(e Entry) {
		k := keyOf(e.Article)
		cur, ok := union[k]
		switch {
		case !ok:
			union[k] = e
			if e.Kind == Quota {
				quota = append(quota, e)
			}
		case e.Kind == Gift && cur.Kind == Quota:
			if e.FirstAt < cur.FirstAt {
				cur.FirstAt = e.FirstAt
			}
			cur.Kind = Gift
			union[k] = cur
			for i, q := range quota {
				if keyOf(q.Article) == k {
					quota = append(quota[:i], quota[i+1:]...)
					break
				}
			}
		case cur.Kind == Gift:
			if e.FirstAt < cur.FirstAt {
				cur.FirstAt = e.FirstAt
			}
			union[k] = cur
		case e.FirstAt < cur.FirstAt:
			cur.FirstAt = e.FirstAt
			union[k] = cur
		}
	}
	for _, e := range ub.recs {
		x.touched++
		add(e)
	}
	for _, e := range db.recs {
		x.touched++
		add(e)
	}

	sort.Slice(quota, func(i, j int) bool {
		if quota[i].FirstAt != quota[j].FirstAt {
			return quota[i].FirstAt < quota[j].FirstAt
		}
		return bytes.Compare(quota[i].Article, quota[j].Article) < 0
	})
	if int64(len(quota)) > x.n {
		for _, e := range quota[x.n:] {
			delete(union, keyOf(e.Article))
		}
	}

	used := 0
	for _, e := range union {
		if e.Kind == Quota {
			used++
		}
	}
	// 用全新的账整体替换用户当月账。
	x.store(user, book{month: x.Month(now), recs: union, used: used})
}

// Entries 返回主体本月账的快照副本（按文章字节序）。
func (x *Meter) Entries(subject []byte, now int64) []Entry {
	bk := x.bookFor(subject, now)
	out := make([]Entry, 0, len(bk.recs))
	for _, e := range bk.recs {
		x.touched++
		e.Article = cloneBytes(e.Article)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return bytes.Compare(out[i].Article, out[j].Article) < 0
	})
	return out
}

// Touched 返回自上次 ResetTouched 以来触碰的解锁记录数。
func (x *Meter) Touched() int { return x.touched }

// ResetTouched 清零触碰计数。
func (x *Meter) ResetTouched() { x.touched = 0 }
