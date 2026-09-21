package upload

import "sync"

type part struct {
	size int64
	etag string
}

// Upload 跟踪一次分片上传的登记状态。
type Upload struct {
	mu        sync.Mutex
	total     int
	minPart   int64
	parts     map[int]part
	received  int
	bytes     int64
	replaced  int
	missing   []int
	completed bool
}

// New 创建一次上传登记。total 为总分片数，minPart 为除最后一片外
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
