package ontology

import (
	"encoding/binary"
	"testing"
)

// 测试用元素生成器：8 字节大端编码加域前缀，保证互不相同。
func elem(domain string, i uint64) []byte {
	b := make([]byte, 0, len(domain)+8)
	b = append(b, domain...)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], i)
	return append(b, num[:]...)
}

const (
	testN     = 10000
	testP     = 0.01
	testQuery = 100000
)

func TestParamsDerivation(t *testing.T) {
	f, err := New(testN, testP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 手算公式：m = ceil(-10000*ln(0.01)/ln2^2) = 95851，k = round(m/n*ln2) = 7。
	if f.M() != 95851 {
		t.Fatalf("M = %d, want 95851", f.M())
	}
	if f.K() != 7 {
		t.Fatalf("K = %d, want 7", f.K())
	}
	if got := len(f.Bytes()); got != (95851+7)/8 {
		t.Fatalf("len(Bytes) = %d, want %d", got, (95851+7)/8)
	}
}

func TestZeroFalseNegatives(t *testing.T) {
	f, err := New(testN, testP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := uint64(0); i < testN; i++ {
		f.Add(elem("in", i))
	}
	for i := uint64(0); i < testN; i++ {
		if !f.MayContain(elem("in", i)) {
			t.Fatalf("false negative at i=%d", i)
		}
	}
}

func TestMeasuredFalsePositiveRate(t *testing.T) {
	f, err := New(testN, testP)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := uint64(0); i < testN; i++ {
		f.Add(elem("in", i))
	}
	fp := 0
	for i := uint64(0); i < testQuery; i++ {
		if f.MayContain(elem("out", i)) {
			fp++
		}
	}
	// 实测假阳性率是随机变量，围绕 p 涨落。[0, 2p] 是宽松上界：
	// 正常实现几乎不可能越界，而哈希退化等错误必然越界。
	rate := float64(fp) / testQuery
	if rate < 0 || rate > 2*testP {
		t.Fatalf("measured FPR = %v, want in [0, %v]", rate, 2*testP)
	}
	t.Logf("measured FPR = %.4f (target p = %v)", rate, testP)
}
