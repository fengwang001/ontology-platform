package logkv

import (
	"path/filepath"
	"testing"
)

// TestDirectoryDistortionSelfHeal 提示信息自带校验通过且有效字节数
// 相符，但条目指向了其他键的记录：读取时发现键/写序号与目录登记
// 不一致（目录失真），报告后扫描该段重建并重试成功。
func TestDirectoryDistortionSelfHeal(t *testing.T) {
	seg1, hint1, seg2 := buildSealedWithHint(t, hintOps)
	h, ok := DecodeHint(hint1)
	if !ok {
		t.Fatal("decode hint")
	}
	if len(h.Entries) < 2 {
		t.Fatalf("need >=2 hint entries, got %d", len(h.Entries))
	}
	// 交换前两个条目的位置信息（保持自带校验有效）。
	h.Entries[0].Offset, h.Entries[1].Offset = h.Entries[1].Offset, h.Entries[0].Offset
	h.Entries[0].Length, h.Entries[1].Length = h.Entries[1].Length, h.Entries[0].Length
	bad := EncodeHint(h)

	s, err := openWith(t, map[string][]byte{
		"000001.seg": seg1, "000001.hint": bad, "000002.seg": seg2,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	if s.Stats().HintSegments != 1 {
		t.Fatalf("hint should be adopted")
	}
	// 第一次读取触发失真、报告并自愈，返回正确结果。
	key0 := string(h.Entries[0].Key)
	val, st, err := s.Get([]byte(key0))
	if err != nil {
		t.Fatalf("get %q after distortion: %v", key0, err)
	}
	if st != StatusFound {
		t.Fatalf("get %q: status=%v", key0, st)
	}
	_ = val
	if got := s.Stats().Distortions; got != 1 {
		t.Fatalf("distortions=%d want 1", got)
	}
	if got := s.Stats().SelfHeals; got != 1 {
		t.Fatalf("selfheals=%d want 1", got)
	}
	// 自愈后该段全部键的结果都正确。
	mustGet(t, s, "k1", "v1-new")
	mustGet(t, s, "k2", "v2")
	mustStatus(t, s, "k3", StatusDeleted)
	mustGet(t, s, "active-key", "av")
	// 再次读取不再产生失真。
	mustGet(t, s, "k1", "v1-new")
	if got := s.Stats().Distortions; got != 1 {
		t.Fatalf("distortions after reheal read=%d want 1", got)
	}
}

// TestDistortionVsCorruption 目录失真与段损坏可区分：采用提示信息
// 的段记录本身校验失败时报段损坏而不是失真。
func TestDistortionVsCorruption(t *testing.T) {
	seg1, hint1, seg2 := buildSealedWithHint(t, hintOps)
	// 破坏段内第一条记录的一个字节（不改变文件大小，提示信息仍被采用）。
	bad := append([]byte(nil), seg1...)
	bad[len(bad)/2] ^= 0x01
	// 找到被破坏字节所属记录对应的键。
	h, _ := DecodeHint(hint1)
	var victim string
	for _, e := range h.Entries {
		if e.Offset <= uint64(len(bad)/2) && uint64(len(bad)/2) < e.Offset+uint64(e.Length) {
			victim = string(e.Key)
		}
	}
	if victim == "" {
		t.Fatal("no victim record found")
	}
	s, err := openWith(t, map[string][]byte{
		"000001.seg": bad, "000001.hint": hint1, "000002.seg": seg2,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	_, _, err = s.Get([]byte(victim))
	if !IsKind(err, KindSegmentCorruption) {
		t.Fatalf("get corrupted key: %v", err)
	}
	if got := s.Stats().Distortions; got != 0 {
		t.Fatalf("distortions=%d want 0 (corruption is not distortion)", got)
	}
}

// TestDistortionReportedOncePerHeal 多次不同键的失真各自报告并自愈。
func TestDistortionPersistAcrossReopen(t *testing.T) {
	seg1, hint1, seg2 := buildSealedWithHint(t, hintOps)
	h, _ := DecodeHint(hint1)
	h.Entries[0].Offset, h.Entries[1].Offset = h.Entries[1].Offset, h.Entries[0].Offset
	h.Entries[0].Length, h.Entries[1].Length = h.Entries[1].Length, h.Entries[0].Length
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "000001.seg"), seg1)
	writeFile(t, filepath.Join(dir, "000001.hint"), EncodeHint(h))
	writeFile(t, filepath.Join(dir, "000002.seg"), seg2)
	s, err := Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	mustGet(t, s, string(h.Entries[0].Key), "v1-new")
	s.Close()
	// 自愈只影响内存目录；提示信息仍在盘上，重开后再次失真并自愈，
	// 结果与上次完全一致（可精确复现）。
	s2, err := Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	mustGet(t, s2, "k1", "v1-new")
	if got := s2.Stats().SelfHeals; got != 1 {
		t.Fatalf("selfheals after reopen=%d want 1", got)
	}
}
