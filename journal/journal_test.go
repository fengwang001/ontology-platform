package journal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/change"
)

func buildLog(t *testing.T, n int) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	j, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		c := change.Change{
			Op: change.Insert, Version: int64(i + 1),
			Group: "g", GroupSet: true, Value: 1.5,
			NewGroup: "n", NewValue: 2.5,
		}
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
	return path, data
}

func TestTruncationClassification(t *testing.T) {
	const n = 200
	_, data := buildLog(t, n)
	total := len(data)
	const frame = 4 + 36 + 4 // 长度前缀 + 定长记录体 + CRC

	// 截断点 1..total-1 逐字节覆盖，按在帧中的偏移决定期望分类。
	for cut := 1; cut < total; cut++ {
		applied := 0
		err := Parse(data[:cut], func(change.Change) error { applied++; return nil })

		wantErr := error(nil)
		switch {
		case cut < headerLen:
			wantErr = ErrHeader
		case cut == headerLen:
			wantErr = nil
		default:
			off := (cut - headerLen) % frame
			switch {
			case off >= 1 && off <= 3:
				wantErr = ErrLength
			case off >= 4 && off <= 39:
				wantErr = ErrBody
			default: // off==0 是完整帧边界；off 40..43 为 CRC 尾
				if off != 0 {
					wantErr = ErrCRC
				}
			}
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("cut=%d off=%d err=%v want %v", cut, (cut-headerLen+frame)%frame, err, wantErr)
		}

		// 生效条数恰为完整帧数；不完整帧绝不生效。
		fullFrames := 0
		if cut >= headerLen {
			fullFrames = (cut - headerLen) / frame
		}
		if applied != fullFrames {
			t.Fatalf("cut=%d applied=%d want %d", cut, applied, fullFrames)
		}
	}

	// 长度完整但内容被改 → CRC 不匹配，前一帧仍生效。
	bad := append([]byte(nil), data...)
	bad[headerLen+4] ^= 0xFF
	applied := 0
	if err := Parse(bad, func(change.Change) error { applied++; return nil }); !errors.Is(err, ErrCRC) {
		t.Fatalf("tamper err=%v want ErrCRC", err)
	}
	if applied != 0 {
		t.Fatalf("tamper applied=%d want 0", applied)
	}
}
