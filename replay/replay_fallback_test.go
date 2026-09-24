package replay

import (
	"os"
	"testing"

	"ontology/sparse"
)

// 索引锚点偏移被篡改（指向记录中间）：定位检出、回退全段扫描、结果正确且有标注。
func TestCorruptIndexFallback(t *testing.T) {
	dir := t.TempDir()
	seg := writeSeg(t, dir, 0, 1000)
	idxPath := seg + ".idx"
	if err := sparse.Build(seg, idxPath, 10); err != nil {
		t.Fatal(err)
	}
	idx, err := sparse.Load(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	idx.Anchors[55].Offset += 3 // 指向记录中间
	if err := os.WriteFile(idxPath, idx.Encode(), 0o644); err != nil {
		t.Fatal(err)
	}
	seqs, st := collect(t, dir, 555, 600)
	if !st.IndexInvalid() {
		t.Fatal("expected IndexInvalid flag")
	}
	if !eqSeqs(seqs, wantSeqs(555, 600)) {
		t.Fatalf("fallback replay wrong, len=%d", len(seqs))
	}
}
