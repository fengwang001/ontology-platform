package upload

import "sync"

// Report 是上传账目的快照。
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

// Upload 记录一次分片上传的登记与完成状态。
type Upload struct {
	mu        sync.Mutex
	total     int
	minPart   int64
	parts     map[int]part
	bytes     int64
	replaced  int
	missing   []int
	completed bool
}

// New 创建一个上传登记器。total 为总分片数，minPart 为除最后一片外
// 每片的最小字节数。
func New(total int, minPart int) *Upload {
	if total < 1 {
		total = 1
	}
	if minPart < 0 {
		minPart = 0
	}
	return &Upload{
		total:   total,
		minPart: int64(minPart),
		parts:   make(map[int]part),
	}
}

// Stat 返回当前账目快照。
func (u *Upload) Stat() Report {
	u.mu.Lock()
	defer u.mu.Unlock()
	r := Report{
		Received: len(u.parts),
		Bytes:    u.bytes,
		Replaced: u.replaced,
	}
	if len(u.missing) > 0 {
		r.Missing = append([]int(nil), u.missing...)
	}
	return r
}
