package serve

import (
	"ontology/coalesce"
	"ontology/rangespec"
	"ontology/source"
)

// Assembler 组装一个 Range 响应并支持断点续写。
//
// 并发约定：不同 Assembler 实例可被不同 goroutine 同时使用；
// 同一个 Assembler 的 Build/WriteTo/查询方法不应并发调用。
// 实例内部以互斥锁保护只读查询与单次写出，但单实例的并发写出不在支持范围。
type Assembler struct {
	cfg Config

	mu       mutex
	total    int64
	ranges   []coalesce.Interval
	body     []byte
	boundary string
	encaps   bool
	built    bool
	written  int64
}

// New 创建一个使用给定配置的组装器。
func New(cfg Config) *Assembler {
	return &Assembler{cfg: cfg.withDefaults()}
}

// Build 解析 Range 头、归一化、从 src 取数并封装。
// 任一阶段失败都会返回错误且不改变已有组装结果（失败后接收器仍是零状态，
// 可再次调用 Build 重试）。超限返回 ErrTooManyRanges / ErrResponseTooLarge /
// ErrBoundaryAttempts；语法错误是 *rangespec.SyntaxError；
// 不可满足是 *coalesce.UnsatisfiableError，可用 errors.As 判定并取得总长。
func (a *Assembler) Build(header string, src source.Source) error {
	cfg := a.cfg

	specs, err := rangespec.Parse(header)
	if err != nil {
		return err
	}
	if len(specs) > cfg.MaxRanges {
		return ErrTooManyRanges
	}

	size := src.Length()
	n := coalesce.NewNormalizer()
	ivs, err := n.Normalize(specs, size)
	if err != nil {
		return err
	}

	encaps := len(ivs) >= 2

	var boundary string
	chunks := make([][]byte, len(ivs))
	contentLen := int64(0)
	for i, iv := range ivs {
		chunk, err := readRange(src, iv)
		if err != nil {
			return err
		}
		chunks[i] = chunk
		contentLen += int64(len(chunk))
	}
	if encaps {
		// 内容只读取一次：先拼接纯内容用于挑选不冲突的边界串，
		// 之后用同一份字节封装，避免"读到一半长度改变"造成两遍不一致。
		content := make([]byte, 0, contentLen)
		for _, chunk := range chunks {
			content = append(content, chunk...)
		}
		b, err := boundaryChooser(content, cfg.MaxBoundaryAttempts)
		if err != nil {
			return ErrBoundaryAttempts
		}
		boundary = b
	}

	body := render(chunks, ivs, size, encaps, boundary)
	if int64(len(body)) > cfg.MaxResponseBytes {
		return ErrResponseTooLarge
	}

	// 全部成功后一次性提交状态；此前任何失败都不会改动 a。
	a.mu.lock()
	a.total = size
	a.ranges = ivs
	a.body = body
	a.boundary = boundary
	a.encaps = encaps
	a.built = true
	a.written = 0
	a.mu.unlock()
	return nil
}
