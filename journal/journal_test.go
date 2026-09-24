package journal

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/change"
)

const testN = 200

func buildLog(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "w.log")
	j, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= testN; i++ {
		c := change.Change{Op: change.Insert, Ver: uint64(i),
			Rec: change.Record{ID: "r001", Group: "g", Value: float64(i)}}
		if err := j.Append(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func replayBytes(t *testing.T, data []byte) (int, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.log")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	n := 0
	err := Replay(path, func(change.Change) error { n++; return nil })
	return n, err
}

func TestReplay(t *testing.T) {
	full := buildLog(t)
	frameSize := (len(full) - headerSize) / testN

	// 正常重放：200 帧全部生效，无错误。
	n, err := replayBytes(t, full)
	if err != nil || n != testN {
		t.Fatalf("full replay: n=%d err=%v", n, err)
	}

	// 逐字节截断：每个截断点按区间分类，并断言生效条数。
	seen := map[error]bool{}
	for cut := 1; cut < len(full); cut++ {
		n, err := replayBytes(t, full[:cut])
		var wantErr error
		switch {
		case cut < headerSize:
			wantErr = ErrShortHeader
		default:
			rem := cut - headerSize
			pos := rem % frameSize
			switch {
			case pos < 4:
				wantErr = ErrShortLength
			case pos < 4+frameSize-4-crcSize:
				wantErr = ErrShortRecord
			case pos < frameSize-crcSize:
				wantErr = ErrShortRecord // 体完整、CRC 字节不足
			default:
				// 体与 CRC 字节都在但内容被截（CRC 必然不符）。
				wantErr = ErrCRC
			}
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("cut=%d: err=%v want %v", cut, err, wantErr)
		}
		wantN := (cut - headerSize) / frameSize
		if n != wantN {
			t.Fatalf("cut=%d: applied=%d want %d", cut, n, wantN)
		}
		seen[wantErr] = true
	}
	for _, e := range []error{ErrShortHeader, ErrShortLength, ErrShortRecord, ErrCRC} {
		if !seen[e] {
			t.Fatalf("分类 %v 未被任何截断点触发", e)
		}
	}

	// 内容翻转：完整长度但 CRC 不符，最后一帧不得生效。
	bad := bytes.Clone(full)
	bad[headerSize+frameSize*(testN-1)+4] ^= 0xFF
	if n, err := replayBytes(t, bad); !errors.Is(err, ErrCRC) || n != testN-1 {
		t.Fatalf("corrupt: n=%d err=%v", n, err)
	}
}
