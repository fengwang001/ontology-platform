package segment

import (
	"errors"
	"io"
	"os"
	"time"

	"ontology/event"
)

// Scanner 顺序扫描一个段文件。读头时校验 headerCRC，
// 与并发写者竞争时短暂重试，保证只见一致前缀。
type Scanner struct {
	f      *os.File
	Header Header
	off    int64
	size   int64
}

// NewScanner 打开 path 并读取段头（失败立即返回，供修复/校验用）。
func NewScanner(path string) (*Scanner, error) { return openScanner(path, 1) }

// NewScannerStable 供与写者并发的读者使用：头不完整或 headerCRC 暂时
// 不符时重试；先读头再取文件大小，保证「count 条记录完整可见」。
func NewScannerStable(path string, retries int) (*Scanner, error) { return openScanner(path, retries) }

func openScanner(path string, retries int) (*Scanner, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	for i := 0; i < retries; i++ {
		buf := make([]byte, HeaderSize)
		if _, err := f.ReadAt(buf, 0); err == nil {
			var h Header
			if h, err = DecodeHeader(buf); err == nil {
				st, serr := f.Stat() // 头先读：count=C 生效时记录 1..C 已落盘
				if serr == nil {
					return &Scanner{f: f, Header: h, off: HeaderSize, size: st.Size()}, nil
				}
			} else if !errors.Is(err, ErrCRCMismatch) {
				f.Close()
				return nil, err // 坏 magic 等永久错误不重试
			}
		}
		if i+1 < retries {
			time.Sleep(time.Millisecond)
		}
	}
	f.Close()
	return nil, ErrHeaderIncomplete
}

// Off 返回当前扫描位置的字节偏移。
func (s *Scanner) Off() int64 { return s.off }

// SeekTo 把扫描位置移动到指定偏移（用于从锚点处继续）。
func (s *Scanner) SeekTo(off int64) { s.off = off }

// Close 关闭底层文件。
func (s *Scanner) Close() error { return s.f.Close() }

// Next 解码当前位置的一条记录并前进；到达文件末尾返回 io.EOF，
// 损坏时返回四类可判定错误之一。
func (s *Scanner) Next() (event.Event, error) {
	if s.off >= s.size {
		return event.Event{}, io.EOF
	}
	remain := int(s.size - s.off)
	buf := make([]byte, min(remain, 4))
	if _, err := s.f.ReadAt(buf, s.off); err != nil {
		return event.Event{}, err
	}
	if len(buf) < 4 {
		return event.Event{}, ErrLengthPrefixIncomplete
	}
	total := 4 + int64(be32(buf)) + 4
	full := make([]byte, min(total, s.size-s.off))
	if _, err := s.f.ReadAt(full, s.off); err != nil {
		return event.Event{}, err
	}
	ev, n, err := DecodeRecord(full)
	if err != nil {
		return event.Event{}, err
	}
	s.off += int64(n)
	return ev, nil
}

// ReadAll 顺序读出段内全部事件（按段头 count 条）。
func ReadAll(path string) ([]event.Event, error) {
	sc, err := NewScanner(path)
	if err != nil {
		return nil, err
	}
	defer sc.Close()
	out := make([]event.Event, 0, sc.Header.Count)
	for i := uint64(0); i < sc.Header.Count; i++ {
		ev, err := sc.Next()
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, nil
}

func be32(b []byte) uint32 { return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]) }
