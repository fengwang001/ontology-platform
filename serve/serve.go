// Package serve 是对外组装器：解析 Range 头、归一化区间、从字节源取数、
// 按需封装 multipart，并支持断点续写。
//
// 并发约定：多个 Assembler 各自组装不同响应时互不干扰、可安全并发；
// 单个 Assembler 不要求并发写出——Write/Info 由互斥锁保护内部一致性，
// 但调用方应串行化对同一 Assembler 的 Write 调用以获得确定的分段语义。
package serve

import (
	"errors"
	"io"
	"sync"

	"ontology/coalesce"
	"ontology/multipart"
	"ontology/rangespec"
	"ontology/source"
)

// 三类资源超限与读取失败的错误，彼此可判定（errors.Is）。
var (
	ErrTooManyRanges = errors.New("serve: range count exceeds limit")
	ErrTooLarge      = errors.New("serve: response bytes exceed limit")
	ErrShortRead     = errors.New("serve: source hit EOF before range was filled")
)

// Config 是组装器的资源上限配置；<=0 的字段取默认值。
type Config struct {
	MaxRanges        int    // 区间个数上限，默认 16
	MaxBytes         int64  // 单次响应总字节上限，默认 16 MiB
	MaxBoundaryTries int    // 边界串生成重试上限，默认 8
	ContentType      string // 分段 Content-Type，默认 application/octet-stream
	// BoundaryGen 非空时用它产生候选边界串（便于确定性测试），
	// 为空时用 crypto/rand 生成 128 bit 随机边界串。
	BoundaryGen func() string
}

func (c Config) withDefaults() Config {
	if c.MaxRanges <= 0 {
		c.MaxRanges = 16
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = 16 << 20
	}
	if c.MaxBoundaryTries <= 0 {
		c.MaxBoundaryTries = 8
	}
	if c.ContentType == "" {
		c.ContentType = "application/octet-stream"
	}
	return c
}

// Info 是只读查询结果，查询不推进任何状态。
type Info struct {
	Ranges     []coalesce.Range // 归一化后的区间列表（副本）
	TotalBytes int64            // 响应总字节数
	Written    int64            // 已写出字节数
	Multipart  bool             // 是否 multipart 封装
}

// Assembler 组装一个响应。未 Prepare 前全部状态为零值。
type Assembler struct {
	mu        sync.Mutex
	cfg       Config
	ranges    []coalesce.Range
	body      []byte
	written   int64
	multipart bool
}

// New 创建组装器。
func New(cfg Config) *Assembler { return &Assembler{cfg: cfg.withDefaults()} }

// Prepare 解析、归一化、取数、封装。任何失败都不会改变已有状态：
// 状态只在全部成功后的临界区内一次性提交。
func (a *Assembler) Prepare(header string, src source.Source) error {
	cfg := a.cfg
	specs, err := rangespec.Parse(header) // 语法错误：*rangespec.ParseError
	if err != nil {
		return err
	}
	if len(specs) > cfg.MaxRanges {
		return ErrTooManyRanges
	}
	total := src.Len()
	ranges, err := coalesce.Normalize(specs, total) // 不可满足：*coalesce.UnsatisfiableError
	if err != nil {
		return err
	}
	contents := make([][]byte, len(ranges))
	for i, r := range ranges {
		buf := make([]byte, r.End-r.Start)
		if err := readFull(src, buf, r.Start); err != nil {
			return err
		}
		contents[i] = buf
	}
	mp := len(ranges) > 1 // 单区间不封装，两个及以上才封装
	body := contents[0]
	if mp {
		if cfg.BoundaryGen != nil {
			body, _, err = multipart.BuildWith(cfg.BoundaryGen, cfg.ContentType, total, ranges, contents, cfg.MaxBoundaryTries)
		} else {
			body, _, err = multipart.Build(cfg.ContentType, total, ranges, contents, cfg.MaxBoundaryTries)
		}
		if err != nil {
			return err // multipart.ErrBoundaryRetries
		}
	}
	if int64(len(body)) > cfg.MaxBytes {
		return ErrTooLarge
	}
	a.mu.Lock()
	a.ranges, a.body, a.written, a.multipart = ranges, body, 0, mp
	a.mu.Unlock()
	return nil
}

// readFull 循环补齐短读，直到 buf 读满；EOF 但字节不足返回 ErrShortRead。
func readFull(src source.Source, buf []byte, off int64) error {
	got := 0
	for got < len(buf) {
		n, err := src.ReadAt(buf[got:], off+int64(got))
		got += n
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ErrShortRead
			}
			return err
		}
		if n == 0 {
			return ErrShortRead // 无进展，防死循环
		}
	}
	return nil
}

// Write 从断点续写：只交付 body[written:] 的字节，写满后返回 io.EOF。
// 不变量：已交付字节恒为 body[:written]，与调用方如何切分 p 无关。
func (a *Assembler) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.written >= int64(len(a.body)) {
		return 0, io.EOF
	}
	n := copy(p, a.body[a.written:])
	a.written += int64(n)
	return n, nil
}

// Info 返回当前状态的快照；连查两次结果相同，查询不推进状态。
func (a *Assembler) Info() Info {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Info{
		Ranges:     append([]coalesce.Range(nil), a.ranges...),
		TotalBytes: int64(len(a.body)),
		Written:    a.written,
		Multipart:  a.multipart,
	}
}
