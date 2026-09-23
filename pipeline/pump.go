package pipeline

import (
	"errors"
	"fmt"

	"ontology/chunker"
	"ontology/resume"
	"ontology/sink"
	"ontology/sizeline"
)

// Resume 用断点与未确认帧序列重建管线，从断点处继续写出。
func Resume(cfg Config, snk sink.Sink, cp resume.Checkpoint, frames [][]byte) (*Pipeline, error) {
	p, err := New(cfg, snk)
	if err != nil {
		return nil, err
	}
	p.ch.Restore(cp.Chunker)
	p.frames = frames
	p.off = cp.FrameOff
	p.accepted = cp.Accepted
	p.confirmed = cp.Confirmed
	p.chunks = cp.Chunks
	p.closed = cp.Closed
	return p, nil
}

// encodeChunks 把切块编码成完整帧（大小行 + 载荷 + CRLF）。
func (p *Pipeline) encodeChunks(chunks []chunker.Chunk) ([][]byte, int, error) {
	var frames [][]byte
	total := 0
	for _, ch := range chunks {
		if len(ch.Data) > p.cfg.MaxChunk {
			return nil, 0, fmt.Errorf("%w: %d > %d",
				ErrChunkTooLarge, len(ch.Data), p.cfg.MaxChunk)
		}
		line := sizeline.Encode(len(ch.Data), p.sexts)
		frame := make([]byte, 0, len(line)+len(ch.Data)+2)
		frame = append(frame, line...)
		frame = append(frame, ch.Data...)
		frame = append(frame, '\r', '\n')
		frames = append(frames, frame)
		total += len(frame)
	}
	return frames, total, nil
}

// pumpLocked 从游标处向下游续写，直到缓冲耗尽或下游停顿。调用须持锁。
func (p *Pipeline) pumpLocked() {
	for len(p.frames) > 0 {
		f := p.frames[0]
		if p.off >= len(f) {
			p.frames = p.frames[1:]
			p.off = 0
			continue
		}
		n, err := p.snk.Write(f[p.off:])
		p.scanBytes += int64(len(f) - p.off)
		if n > 0 {
			p.off += n
			p.confirmed += int64(n)
		}
		switch {
		case err != nil && !errors.Is(err, sink.ErrBackpressure) && !errors.Is(err, sink.ErrDisconnected):
			p.lastErr = err
			return
		case err != nil:
			return // 背压或断开：保留游标之后的全部字节
		case n == 0:
			return // 防御：避免空转
		}
	}
}
