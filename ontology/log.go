package ontology

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sync"
)

// 帧格式: [4B 小端 payload 长度][4B CRC32(IEEE) of payload][payload(JSON)]。
// 任何长度不足、CRC 不匹配或序号不连续的最后一条记录都被视为撕裂写入，
// 整体丢弃，绝不部分生效。
const frameHeaderSize = 8

// logWriter 以 [len][crc][payload] 帧格式顺序追加日志项。
type logWriter struct {
	mu   sync.Mutex
	file *os.File
}

// openLogWriter 打开（或创建）日志文件用于追加。
func openLogWriter(path string) (*logWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &logWriter{file: f}, nil
}

// append 写入一条完整日志项并 fsync。
func (w *logWriter) append(entry *LogEntry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	frame := make([]byte, frameHeaderSize+len(payload))
	binary.LittleEndian.PutUint32(frame[0:4], uint32(len(payload)))
	binary.LittleEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	copy(frame[frameHeaderSize:], payload)
	if _, err := w.file.Write(frame); err != nil {
		return err
	}
	return w.file.Sync()
}

// offset 返回当前文件末尾偏移。
func (w *logWriter) offset() (int64, error) {
	return w.file.Seek(0, io.SeekEnd)
}

// truncate 将日志截断到指定偏移（用于丢弃撕裂的尾部）。
func (w *logWriter) truncate(size int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.file.Truncate(size); err != nil {
		return err
	}
	if _, err := w.file.Seek(size, io.SeekStart); err != nil {
		return err
	}
	return nil
}

func (w *logWriter) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

// readLog 从头顺序读取日志，返回完整有效的日志项与有效字节数。
// 遇到不完整或校验失败的尾部记录时停止，该记录被整体丢弃。
// 序号必须从 1 开始严格连续递增，否则视为损坏并停止（与撕裂同等处理）。
func readLog(path string) (entries []*LogEntry, validBytes int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()

	var offset int64
	var expectSeq uint64 = 1
	for {
		header := make([]byte, frameHeaderSize)
		n, readErr := io.ReadFull(f, header)
		if readErr != nil {
			if readErr == io.EOF && n == 0 {
				return entries, offset, nil // 干净结束
			}
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				return entries, offset, nil // 撕裂的帧头：丢弃
			}
			return nil, 0, readErr
		}
		payloadLen := binary.LittleEndian.Uint32(header[0:4])
		wantCRC := binary.LittleEndian.Uint32(header[4:8])
		if payloadLen > 1<<20 { // 防御性上限，超出即视为损坏
			return entries, offset, nil
		}
		payload := make([]byte, payloadLen)
		if _, readErr := io.ReadFull(f, payload); readErr != nil {
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				return entries, offset, nil // 撕裂的载荷：丢弃
			}
			return nil, 0, readErr
		}
		if crc32.ChecksumIEEE(payload) != wantCRC {
			return entries, offset, nil // 校验失败：丢弃该记录及其后内容
		}
		var entry LogEntry
		if jsonErr := json.Unmarshal(payload, &entry); jsonErr != nil {
			return entries, offset, nil
		}
		if entry.Seq != expectSeq {
			return nil, 0, fmt.Errorf("log corrupted: seq gap or dup, want %d got %d", expectSeq, entry.Seq)
		}
		entries = append(entries, &entry)
		expectSeq++
		offset += int64(frameHeaderSize) + int64(payloadLen)
	}
}
