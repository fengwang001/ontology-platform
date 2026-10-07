package chunkcache

import "sync"

// Config 为缓存配置：C 为最大缓存切片数，S 为切片字节数。
type Config struct {
	S      int64
	C      int
	Origin Origin
	Logger Logger
}

// ChunkSource 标明响应切片来源。
type ChunkSource int

const (
	SourceOrigin ChunkSource = iota
	SourceCache
)

// ChunkReport 逐切片说明来源。
type ChunkReport struct {
	Index   int
	Version string
	Source  ChunkSource
}

// OriginCallRecord 记录本次请求实际发出的一次回源。
type OriginCallRecord struct {
	First           int
	Last            int
	ExpectedVersion string
	GotVersion      string
}

// GetResponse 为一次请求的应答。
type GetResponse struct {
	Version     string
	Data        []byte
	Chunks      []ChunkReport
	OriginCalls []OriginCallRecord
	gens        map[int64]struct{}
}

// Stats 暴露可验证的计数器。
type Stats struct {
	OriginCalls    int64
	CacheHits      int64
	OriginChunks   int64
	Rejudges       int64
	RejectedNoCall int64
}

// Cache 为分片缓存（骨架实现）。
type Cache struct {
	s      int64
	c      int
	origin Origin
	log    Logger

	mu sync.Mutex
	st *state
	// flightRegs 按对象键保存“下标 -> 在途 flight”，所有访问均持 mu。
	flightRegs map[string]map[int]*flight

	stats Stats
}

// New 构造缓存。S 或 C 非正数属参数非法。
func New(cfg Config) (*Cache, error) {
	if cfg.S <= 0 {
		return nil, newError(KindInvalidParam, "New", ReasonNonPositiveS, nil)
	}
	if cfg.C <= 0 {
		return nil, newError(KindInvalidParam, "New", ReasonNonPositiveC, nil)
	}
	if cfg.Origin == nil {
		return nil, newError(KindInvalidParam, "New", "nil_origin", nil)
	}
	log := cfg.Logger
	if log == nil {
		log = nopLogger{}
	}
	return &Cache{
		s:          cfg.S,
		c:          cfg.C,
		origin:     cfg.Origin,
		log:        log,
		st:         newState(),
		flightRegs: make(map[string]map[int]*flight),
	}, nil
}

// Stats 返回计数器快照。
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}
