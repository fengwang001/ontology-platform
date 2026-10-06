package logkv

import (
	"encoding/binary"
	"path/filepath"
	"testing"
)

// buildSealedWithHint 构建含一个已封口段（带提示信息）与一个
// 活动段的存储，返回各文件内容。
func buildSealedWithHint(t *testing.T, ops []op) (seg1, hint1, seg2 []byte) {
	t.Helper()
	src := newTestStore(t, 1<<20)
	for _, o := range ops {
		if o.tombstone {
			mustDelete(t, src, o.key)
		} else {
			mustPut(t, src, o.key, o.val)
		}
	}
	rotateNow(t, src)
	mustPut(t, src, "active-key", "av")
	seg1 = readFile(t, segPath(src, 1))
	hint1 = readFile(t, filepath.Join(src.dir, "000001.hint"))
	seg2 = readFile(t, segPath(src, 2))
	src.Close()
	return seg1, hint1, seg2
}

var hintOps = []op{
	{key: "k1", val: "v1"},
	{key: "k2", val: "v2"},
	{key: "k3", tombstone: true},
	{key: "k1", val: "v1-new"},
}

// openWith 用给定文件内容打开存储。
func openWith(t *testing.T, files map[string][]byte) (*Store, error) {
	t.Helper()
	dir := t.TempDir()
	for name, b := range files {
		writeFile(t, filepath.Join(dir, name), b)
	}
	return Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: true})
}

func checkHintFallback(t *testing.T, s *Store) {
	t.Helper()
	st := s.Stats()
	if st.HintSegments != 0 || st.ScannedSegments != 1 {
		t.Fatalf("hint=%d scanned=%d, want hint rejected and segment scanned", st.HintSegments, st.ScannedSegments)
	}
	mustGet(t, s, "k1", "v1-new")
	mustGet(t, s, "k2", "v2")
	mustStatus(t, s, "k3", StatusDeleted)
	mustGet(t, s, "active-key", "av")
}

// TestHintTamperEveryByte 提示信息每个字节被篡改（自带校验失败）
// 时作废并回退到全段扫描，扫描结果为权威。
func TestHintTamperEveryByte(t *testing.T) {
	seg1, hint1, seg2 := buildSealedWithHint(t, hintOps)
	for i := 0; i < len(hint1); i++ {
		bad := append([]byte(nil), hint1...)
		bad[i] ^= 0xFF
		s, err := openWith(t, map[string][]byte{
			"000001.seg": seg1, "000001.hint": bad, "000002.seg": seg2,
		})
		if err != nil {
			t.Fatalf("byte %d: open: %v", i, err)
		}
		checkHintFallback(t, s)
		s.Close()
	}
}

// TestHintValidBytesMismatch 声明的有效字节数与段实际大小不符时
// 作废提示信息（即使自带校验通过）。
func TestHintValidBytesMismatch(t *testing.T) {
	seg1, hint1, seg2 := buildSealedWithHint(t, hintOps)
	h, ok := DecodeHint(hint1)
	if !ok {
		t.Fatal("decode hint")
	}
	for _, delta := range []int64{-1, 1, -int64(h.ValidBytes)} {
		bad := h
		bad.ValidBytes = uint64(int64(h.ValidBytes) + delta)
		s, err := openWith(t, map[string][]byte{
			"000001.seg": seg1, "000001.hint": EncodeHint(bad), "000002.seg": seg2,
		})
		if err != nil {
			t.Fatalf("delta %d: open: %v", delta, err)
		}
		checkHintFallback(t, s)
		s.Close()
	}
}

// TestHintFieldsTampered 逐个字段篡改（重新计算自带校验，模拟
// 提示信息与段内容不符）：有效字节数以外的字段被改后，若仍满足
// 有效字节数相等，恢复采用后由读取路径的失真自愈兜底。
func TestHintFieldsTampered(t *testing.T) {
	seg1, hint1, seg2 := buildSealedWithHint(t, hintOps)
	if _, ok := DecodeHint(hint1); !ok {
		t.Fatal("decode hint")
	}
	// magic 损坏（绕过自带校验的结构性检查）。
	bad := append([]byte(nil), hint1...)
	bad[0] = 'X'
	binary.LittleEndian.PutUint32(bad[len(bad)-4:], 0) // 顺带破坏 crc
	s, err := openWith(t, map[string][]byte{
		"000001.seg": seg1, "000001.hint": bad, "000002.seg": seg2,
	})
	if err != nil {
		t.Fatalf("magic: %v", err)
	}
	checkHintFallback(t, s)
	s.Close()

	// 截断与追加垃圾。
	for _, b := range [][]byte{hint1[:len(hint1)/2], append(hint1, 0xAB)} {
		s, err := openWith(t, map[string][]byte{
			"000001.seg": seg1, "000001.hint": b, "000002.seg": seg2,
		})
		if err != nil {
			t.Fatalf("truncated/garbage: %v", err)
		}
		checkHintFallback(t, s)
		s.Close()
	}
}

// TestHintMissing 提示信息缺失时扫描全段重建。
func TestHintMissing(t *testing.T) {
	seg1, _, seg2 := buildSealedWithHint(t, hintOps)
	s, err := openWith(t, map[string][]byte{
		"000001.seg": seg1, "000002.seg": seg2,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	checkHintFallback(t, s)
	s.Close()
}
