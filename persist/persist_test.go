package persist_test

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"ontology/persist"
	"ontology/search"
	"ontology/vec"
)

const (
	dim, bits, nTab, nVec = 4, 4, 2, 40
	planesEnd             = persist.HeaderSize + nTab*bits*dim*8
)

// fixture 建一个小索引并落盘，返回文件字节与路径。
func fixture(t *testing.T) ([]byte, string) {
	t.Helper()
	vs := make([]vec.Vec, nVec)
	for i := range vs {
		vs[i] = vec.Vec{float64(i%7) - 3, float64(i % 5), float64(i), 1}
	}
	ix, err := search.Build(vs, dim, bits, nTab, 77)
	if err != nil {
		t.Fatal(err)
	}
	fams, set := ix.Snapshot()
	pi := &persist.Index{Dim: dim, Bits: bits, NVecs: ix.NumVecs(), Seed: 77}
	for _, f := range fams {
		pi.Planes = append(pi.Planes, f.Planes())
	}
	for _, tb := range set.Tables {
		pi.Tables = append(pi.Tables, tb.Buckets())
	}
	p := filepath.Join(t.TempDir(), "idx.bin")
	if err := persist.Save(p, pi); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return raw, p
}

// classify 返回截断点 at 应有的错误类别。
func classify(at, total int) error {
	switch {
	case at < persist.HeaderSize:
		return persist.ErrTruncHeader
	case at < planesEnd:
		return persist.ErrTruncPlanes
	case at < total-4:
		return persist.ErrTruncBuckets
	default:
		return persist.ErrCRC
	}
}

func writeCut(t *testing.T, raw []byte, at int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cut")
	if err := os.WriteFile(p, raw[:at], 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTruncateClassify(t *testing.T) {
	raw, _ := fixture(t)
	counts := map[error]int{}
	for at := 1; at < len(raw); at++ { // 逐字节遍历全部截断点
		_, err := persist.Load(writeCut(t, raw, at))
		if want := classify(at, len(raw)); !errors.Is(err, want) {
			t.Fatalf("截断点 %d: got %v, want %v", at, err, want)
		}
		counts[classify(at, len(raw))]++
	}
	t.Logf("文件 %d 字节: 头[0,%d) 超平面[%d,%d) 桶表[%d,%d) CRC[%d,%d)",
		len(raw), persist.HeaderSize, persist.HeaderSize, planesEnd, planesEnd, len(raw)-4, len(raw)-4, len(raw))
	for _, e := range []error{persist.ErrTruncHeader, persist.ErrTruncPlanes, persist.ErrTruncBuckets, persist.ErrCRC} {
		if counts[e] == 0 {
			t.Errorf("类别 %v 未被任何截断点命中", e)
		}
		t.Logf("CLASS %v: %d 个截断点", e, counts[e])
	}
}

func TestRecoverPrefix(t *testing.T) {
	raw, _ := fixture(t)
	for at := 1; at < len(raw); at++ {
		rec, st, err := persist.Recover(writeCut(t, raw, at))
		if at < persist.HeaderSize {
			if !errors.Is(err, persist.ErrTruncHeader) {
				t.Fatalf("截断点 %d: Recover err=%v, want ErrTruncHeader", at, err)
			}
			continue
		}
		if err != nil || st.TablesRecovered != len(rec.Tables) || len(rec.Planes) != len(rec.Tables) {
			t.Fatalf("截断点 %d: 恢复结果不自洽 err=%v st=%+v", at, err, st)
		}
		for _, tab := range rec.Tables { // 不得留悬挂 ID
			for _, ids := range tab {
				for _, id := range ids {
					if id < 0 || id >= rec.NVecs {
						t.Fatalf("截断点 %d: 悬挂 ID %d 未被剔除", at, id)
					}
				}
			}
		}
		if at < planesEnd && st.TablesRecovered != 0 {
			t.Fatalf("截断点 %d 在超平面区, 不应恢复出表", at)
		}
	}
	// 代表点日志：供 FINDINGS 记录恢复桶数
	for _, at := range []int{planesEnd - 1, planesEnd + 5, len(raw) - 100, len(raw) - 1} {
		rec, st, _ := persist.Recover(writeCut(t, raw, at))
		nb := 0
		for _, tab := range rec.Tables {
			nb += len(tab)
		}
		t.Logf("RECOVER cut=%d tables=%d buckets=%d dangling=%d", at, st.TablesRecovered, nb, st.DanglingIDs)
	}
	// 人为制造悬挂 ID：恢复必须剔除并计数
	bad := append([]byte(nil), raw...)
	binary.LittleEndian.PutUint32(bad[planesEnd+16:], math.MaxUint32)
	p := writeCut(t, bad, len(bad))
	rec, st, err := persist.Recover(p)
	if err != nil || st.DanglingIDs < 1 {
		t.Fatalf("悬挂 ID 未被计数: st=%+v err=%v", st, err)
	}
	for _, tab := range rec.Tables {
		for _, ids := range tab {
			for _, id := range ids {
				if id >= rec.NVecs {
					t.Fatalf("悬挂 ID %d 恢复后仍存在", id)
				}
			}
		}
	}
}

func TestLoadDamage(t *testing.T) {
	raw, full := fixture(t)
	idx, err := persist.Load(full)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := persist.Load(full)
	if !reflect.DeepEqual(idx, again) || idx.Dim != dim || idx.Bits != bits || idx.NVecs != nVec {
		t.Fatal("读回结果与自描述头不一致")
	}
	cases := []struct {
		name   string
		mutate func(b []byte)
		want   error
	}{
		{"坏魔数", func(b []byte) { b[0] = 'X' }, persist.ErrBadMagic},
		{"CRC翻转", func(b []byte) { b[len(b)-1] ^= 0xff }, persist.ErrCRC},
		{"退化超平面", func(b []byte) {
			off := persist.HeaderSize + (1*bits+2)*dim*8 // 第 1 张表第 2 位
			for i := 0; i < dim*8; i++ {
				b[off+i] = 0
			}
		}, persist.ErrDegenerate},
	}
	for _, c := range cases {
		b := append([]byte(nil), raw...)
		c.mutate(b)
		_, err := persist.Load(writeCut(t, b, len(b)))
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
		if c.want == persist.ErrDegenerate && err != nil {
			wantMsg := "table 1 bit 2"
			if got := err.Error(); !contains(got, wantMsg) {
				t.Errorf("退化错误应指出位置 %q, got %q", wantMsg, got)
			}
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
