package upload

import (
	"sort"
	"sync"
)

// Report 描述一次上传当前的账目快照。
type Report struct {
	Received int   // 当前已登记的分片数
	Bytes    int64 // 已登记分片的字节总和
	Replaced int   // 累计被覆盖的分片次数
	Missing  []int // 完成校验时缺少的分片号，升序
}

type part struct {
	size int64
	etag string
}

// Upload 记录一次分片上传的登记状态，并发安全。
type Upload struct {
	mu        sync.Mutex
	total     int
	minPart   int
	parts     map[int]part
	replaced  int
	missing   []int
	completed bool
}

// New 创建一次上传登记。total 为总分片数，minPart 为除最后一片外
// 每片的最小字节数。total 小于 1 时按 1 处理，minPart 小于 0 时按 0 处理。
func New(total int, minPart int) *Upload {
	if total < 1 {
		total = 1
	}
	if minPart < 0 {
		minPart = 0
	}
	return &Upload{
		total:   total,
		minPart: minPart,
		parts:   make(map[int]part),
	}
}

// Put 登记第 n 片（从 1 起），etag 由调用方给出。
// 同一分片重复登记视为覆盖：Received 不变、Bytes 换旧为新、Replaced 加一。
func (u *Upload) Put(n int, size int64, etag string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.completed {
		return ErrCompleted
	}
	if n < 1 || n > u.total || size <= 0 {
		return ErrBadPart
	}
	if n != u.total && size < int64(u.minPart) {
		return ErrBadPart
	}
	if _, ok := u.parts[n]; ok {
		u.replaced++
	}
	u.parts[n] = part{size: size, etag: etag}
	return nil
}

// Complete 校验并完成上传；etags 按分片号升序给出，长度必须等于 total。
// 缺片优先于 etag 比对：有缺片时返回 ErrMissingPart 且不检查 etag。
// 任一失败都不会丢弃已登记的分片，可补传后再次 Complete。
func (u *Upload) Complete(etags []string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.completed {
		return ErrCompleted
	}
	u.missing = u.missing[:0]
	for n := 1; n <= u.total; n++ {
		if _, ok := u.parts[n]; !ok {
			u.missing = append(u.missing, n)
		}
	}
	if len(u.missing) > 0 {
		return ErrMissingPart
	}
	if len(etags) != u.total {
		return ErrEtagMismatch
	}
	for i, etag := range etags {
		if u.parts[i+1].etag != etag {
			return ErrEtagMismatch
		}
	}
	u.completed = true
	return nil
}

// Stat 返回当前账目快照。Missing 为最近一次 Complete 检出的缺失分片号，
// 升序排列；从未因缺片失败过时为 nil。
func (u *Upload) Stat() Report {
	u.mu.Lock()
	defer u.mu.Unlock()
	var bytes int64
	for _, p := range u.parts {
		bytes += p.size
	}
	var missing []int
	if len(u.missing) > 0 {
		missing = make([]int, len(u.missing))
		copy(missing, u.missing)
		sort.Ints(missing)
	}
	return Report{
		Received: len(u.parts),
		Bytes:    bytes,
		Replaced: u.replaced,
		Missing:  missing,
	}
}
