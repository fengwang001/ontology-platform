// Package pipeline 串起切块、编码、背压处理、断点续写与收尾，
// 对外提供并发安全的分块流式写出管线。
package pipeline

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"ontology/chunker"
	"ontology/resume"
	"ontology/sink"
	"ontology/sizeline"
)

// 三类可判定的拒绝/状态错误。
var (
	ErrClosed        = errors.New("pipeline: write after close")
	ErrChunkTooLarge = errors.New("pipeline: chunk too large")
	ErrExtTooLarge   = errors.New("pipeline: extensions too large")
	// ErrBackpressure 缓冲超限或下游暂时不可写；数据保留，可重试。
	ErrBackpressure = sink.ErrBackpressure
)

// Config 配置管线。
type Config struct {
	MinChunk   int           // 聚合下限（见 chunker）
	MaxChunk   int           // 满块大小，超过的单次写入被切分
	Window     time.Duration // 聚合时间窗
	MaxWrite   int           // 单次 Write 硬上限，0 表示不限
	MaxPending int           // 待写缓冲上限（编码字节），0 表示不限
	MaxExtLen  int           // 扩展编码总长上限，0 表示不限
	Exts       []chunker.Ext // 附加到每个数据块的扩展
	Clock      chunker.Clock // 注入时钟
}

// Stats 是只读查询结果；同一时刻连查两次完全相同。
type Stats struct {
	Accepted  int64 // 已接受的编码字节数
	Confirmed int64 // 已确认写出的编码字节数
	Pending   int64 // 待写缓冲字节数，恒等于 Accepted-Confirmed
	Chunks    int64 // 已产生的数据块数（不含结束块）
	Closed    bool  // 是否已结束
}

// Pipeline 是分块流式写出管线，所有方法并发安全。
type Pipeline struct {
	mu        sync.Mutex
	snk       sink.Sink
	cfg       Config
	sexts     []sizeline.Ext // 预转换的扩展，避免每次编码重复转换
	ch        *chunker.Chunker
	frames    [][]byte // 未确认完的编码帧队列，frames[0] 为当前帧
	off       int      // 当前帧内已确认偏移
	accepted  int64
	confirmed int64
	chunks    int64
	closed    bool
	scanBytes int64 // 非导出计数器：历次推进提交给下游的字节数
	lastErr   error
}

// New 校验配置并创建管线。
func New(cfg Config, snk sink.Sink) (*Pipeline, error) {
	if snk == nil {
		return nil, errors.New("pipeline: sink is required")
	}
	ch, err := chunker.New(chunker.Config{
		MinChunk: cfg.MinChunk,
		MaxChunk: cfg.MaxChunk,
		Window:   cfg.Window,
		Exts:     cfg.Exts,
		Clock:    cfg.Clock,
	})
	if err != nil {
		return nil, err
	}
	p := &Pipeline{snk: snk, cfg: cfg, ch: ch}
	for _, e := range cfg.Exts {
		p.sexts = append(p.sexts, sizeline.Ext{Key: e.Key, Value: e.Value})
	}
	return p, nil
}

// Write 接收上游字节；零长写入不产块；缓冲超限报背压且不改状态。
func (p *Pipeline) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, ErrClosed
	}
	if len(b) == 0 {
		return 0, nil
	}
	if p.cfg.MaxWrite > 0 && len(b) > p.cfg.MaxWrite {
		return 0, fmt.Errorf("%w: %d > %d", ErrChunkTooLarge, len(b), p.cfg.MaxWrite)
	}
	if err := p.checkExtsLocked(); err != nil {
		return 0, err
	}
	snap := p.ch.Save()
	chunks, err := p.ch.Write(b)
	if err != nil {
		return 0, err
	}
	frames, total, err := p.encodeChunks(chunks)
	if err != nil {
		p.ch.Restore(snap)
		return 0, err
	}
	if p.cfg.MaxPending > 0 && p.pendingLocked()+int64(total) > int64(p.cfg.MaxPending) {
		p.ch.Restore(snap)
		return 0, ErrBackpressure
	}
	p.enqueueLocked(frames, total, len(chunks))
	p.pumpLocked()
	return len(b), nil
}

// Advance 推进写出：先按注入时钟做窗口聚合，再向下游续写。
func (p *Pipeline) Advance() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastErr != nil {
		return p.lastErr
	}
	if !p.closed {
		snap := p.ch.Save()
		chunks := p.ch.Tick()
		frames, total, err := p.encodeChunks(chunks)
		if err != nil {
			p.ch.Restore(snap)
			return err
		}
		if p.cfg.MaxPending > 0 && p.pendingLocked()+int64(total) > int64(p.cfg.MaxPending) {
			p.ch.Restore(snap)
			return ErrBackpressure
		}
		p.enqueueLocked(frames, total, len(chunks))
	}
	p.pumpLocked()
	return p.lastErr
}

// Close 入队结束块（大小 0 + 空行）并推进；重复调用幂等。
func (p *Pipeline) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	chunks := p.ch.Close()
	frames, total, err := p.encodeChunks(chunks)
	if err != nil {
		return err
	}
	nData := len(frames)
	end := sizeline.Encode(0, nil)
	end = append(end, '\r', '\n')
	frames = append(frames, end)
	total += len(end)
	p.enqueueLocked(frames, total, nData)
	p.pumpLocked()
	return p.lastErr
}

// Stats 只读查询，不推进任何状态。
func (p *Pipeline) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{
		Accepted:  p.accepted,
		Confirmed: p.confirmed,
		Pending:   p.accepted - p.confirmed,
		Chunks:    p.chunks,
		Closed:    p.closed,
	}
}

// ScanBytes 返回历次推进提交给下游的总字节数（只读）。
func (p *Pipeline) ScanBytes() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scanBytes
}

// Checkpoint 导出断点与未确认帧序列，供断开后 Resume 续写。
func (p *Pipeline) Checkpoint() (resume.Checkpoint, [][]byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := resume.Checkpoint{
		Confirmed: p.confirmed,
		Accepted:  p.accepted,
		Chunks:    p.chunks,
		FrameOff:  p.off,
		Closed:    p.closed,
		Chunker:   p.ch.Save(),
	}
	frames := make([][]byte, len(p.frames))
	copy(frames, p.frames)
	return cp, frames
}

// pendingLocked 返回待写缓冲字节数；不变量：Accepted == Confirmed + Pending。
func (p *Pipeline) pendingLocked() int64 { return p.accepted - p.confirmed }

// enqueueLocked 把编码帧挂到队尾并记账。调用须持锁。
func (p *Pipeline) enqueueLocked(frames [][]byte, total, dataChunks int) {
	p.frames = append(p.frames, frames...)
	p.accepted += int64(total)
	p.chunks += int64(dataChunks)
}

// checkExtsLocked 在写入前判定扩展总长是否超限；扩展为固定配置，该判定在
// 任何数据块产生之前即可确定，拒绝时不触碰任何状态。
func (p *Pipeline) checkExtsLocked() error {
	if p.cfg.MaxExtLen > 0 {
		if n := sizeline.EncodedExtLen(p.sexts); n > p.cfg.MaxExtLen {
			return fmt.Errorf("%w: %d > %d", ErrExtTooLarge, n, p.cfg.MaxExtLen)
		}
	}
	return nil
}
