package patch_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	"ontology/chunk"
	"ontology/patch"
	"ontology/verify"
)

// 手工构造的补丁：目标 "AAAABBBBCCCC"（3 块），源 "AAAAXYBBBBZ"。
// 布局：头 36B | reuse0(5B) | data"XY"(7B) | reuse1(5B) | data"Z"(6B) | CRC(4B) = 63B。
func sample() (target, src, wire []byte) {
	target = []byte("AAAABBBBCCCC")
	src = []byte("AAAAXYBBBBZ")
	p := patch.Patch{
		BlockSize: 4,
		SrcLen:    len(src),
		SrcHash:   chunk.Strong(src),
		Instrs: []patch.Instr{
			{Op: patch.OpReuse, Block: 0},
			{Op: patch.OpData, Data: []byte("XY")},
			{Op: patch.OpReuse, Block: 1},
			{Op: patch.OpData, Data: []byte("Z")},
		},
	}
	return target, src, patch.Encode(p)
}

func classOf(err error) string {
	switch {
	case errors.Is(err, verify.ErrHeaderIncomplete):
		return "header"
	case errors.Is(err, verify.ErrInstrIncomplete):
		return "instr"
	case errors.Is(err, verify.ErrDataIncomplete):
		return "data"
	case errors.Is(err, verify.ErrCRCMismatch):
		return "crc"
	}
	return ""
}

// wantClass 给出每个截断点（保留前 cut 字节）应有的分类。
func wantClass(cut int) string {
	switch {
	case cut < 36:
		return "header"
	case cut == 46 || cut == 47 || cut == 58:
		return "data"
	case cut >= 59:
		return "crc"
	default:
		return "instr"
	}
}

func TestPatch(t *testing.T) {
	target, src, wire := sample()
	if len(wire) != 63 {
		t.Fatalf("wire len=%d want 63", len(wire))
	}

	t.Run("truncation classified and never partially applied", func(t *testing.T) {
		dir := t.TempDir()
		tp := filepath.Join(dir, "target")
		pp := filepath.Join(dir, "patch")
		if err := os.WriteFile(tp, target, 0o644); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for cut := 1; cut < len(wire); cut++ {
			if _, err := patch.Apply(target, wire[:cut]); err == nil {
				t.Fatalf("cut %d: expected error", cut)
			} else if got, want := classOf(err), wantClass(cut); got != want {
				t.Errorf("cut %d: class %q want %q", cut, got, want)
			} else {
				seen[got] = true
			}
			if err := os.WriteFile(pp, wire[:cut], 0o644); err != nil {
				t.Fatal(err)
			}
			if err := patch.ApplyFile(tp, pp); err == nil {
				t.Fatalf("cut %d: ApplyFile expected error", cut)
			}
			got, _ := os.ReadFile(tp)
			if !bytes.Equal(got, target) {
				t.Fatalf("cut %d: target file modified after failed apply", cut)
			}
		}
		for _, c := range []string{"header", "instr", "data", "crc"} {
			if !seen[c] {
				t.Errorf("class %q never observed", c)
			}
		}
	})

	t.Run("intact patch applies to exact source", func(t *testing.T) {
		out, err := patch.Apply(target, wire)
		if err != nil || !bytes.Equal(out, src) {
			t.Fatalf("out=%q err=%v", out, err)
		}
	})

	fixCRC := func(b []byte) []byte {
		binary.BigEndian.PutUint32(b[len(b)-4:], crc32.ChecksumIEEE(b[:len(b)-4]))
		return b
	}
	clone := func(b []byte) []byte { return append([]byte(nil), b...) }

	t.Run("corruption detected", func(t *testing.T) {
		cases := []struct {
			name   string
			wire   func() []byte
			target []byte
			want   error
		}{
			{"reuse block out of range", func() []byte {
				w := clone(wire)
				binary.BigEndian.PutUint32(w[37:41], 999)
				return fixCRC(w)
			}, target, verify.ErrOutOfRange},
			{"payload byte corrupted", func() []byte {
				w := clone(wire)
				w[46] ^= 0xFF
				return w
			}, target, verify.ErrCRCMismatch},
			{"target changed after signature", func() []byte {
				return clone(wire)
			}, []byte("XAAABBBBCCCC"), verify.ErrMismatch},
			{"bad magic", func() []byte {
				w := clone(wire)
				w[0] = 'X'
				return fixCRC(w)
			}, target, verify.ErrHeaderIncomplete},
		}
		for _, tc := range cases {
			_, err := patch.Apply(tc.target, tc.wire())
			if !errors.Is(err, tc.want) {
				t.Errorf("%s: err=%v want %v", tc.name, err, tc.want)
			}
		}
	})
}
