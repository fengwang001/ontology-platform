package swingdoor

import (
	"bytes"
	"math/big"
	"testing"
)

func mustWrite(t *testing.T, c *Compressor, pts ...Sample) {
	t.Helper()
	for _, p := range pts {
		if err := c.Write(p.T, p.V); err != nil {
			t.Fatalf("Write(%d,%d): %v", p.T, p.V, err)
		}
	}
}

func samplesEq(a, b []Sample) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// naiveVerify 用有理数逐点校验：
//  1. 首尾采样都在存档点中，存档点时间戳严格递增且都是输入中的点；
//  2. 每个被丢弃点相对其前后相邻存档点连线的纵向偏差不超过 E。
func naiveVerify(t *testing.T, e int64, input, archives []Sample) {
	t.Helper()

	if len(input) == 0 {
		t.Fatal("empty input")
	}
	if len(archives) < 1 {
		t.Fatal("no archives")
	}
	if archives[0] != input[0] {
		t.Fatalf("first sample must be archived: got %v want %v", archives[0], input[0])
	}
	if archives[len(archives)-1] != input[len(input)-1] {
		t.Fatalf("last sample must be archived: got %v want %v",
			archives[len(archives)-1], input[len(input)-1])
	}

	inSet := make(map[Sample]bool, len(input))
	for _, p := range input {
		inSet[p] = true
	}
	for i, a := range archives {
		if !inSet[a] {
			t.Fatalf("archive %v not in input", a)
		}
		if i > 0 && a.T <= archives[i-1].T {
			t.Fatalf("archive timestamps not strictly increasing: %v", archives)
		}
	}

	archSet := make(map[Sample]bool, len(archives))
	for _, a := range archives {
		archSet[a] = true
	}

	eb := big.NewInt(e)
	for ai := 0; ai+1 < len(archives); ai++ {
		a, b := archives[ai], archives[ai+1]
		dt := big.NewInt(b.T - a.T) // 正数
		for _, p := range input {
			if p.T <= a.T || p.T >= b.T || archSet[p] {
				continue
			}
			// 连线在 p.T 处的分子（分母为 dt）：
			// a.V*dt + (b.V-a.V)*(p.T-a.T)
			lineNum := new(big.Int).Mul(big.NewInt(a.V), dt)
			lineNum.Add(lineNum,
				new(big.Int).Mul(big.NewInt(b.V-a.V), big.NewInt(p.T-a.T)))
			// 连线值为 lineNum/dt，与 p.V 同刻度比较：|p.V*dt - lineNum|。
			diff := new(big.Int).Sub(new(big.Int).Mul(big.NewInt(p.V), dt), lineNum)
			diff.Abs(diff)
			// |p.V - line| <= E  <=>  diff <= E*dt
			lhs := diff
			rhs := new(big.Int).Mul(eb, dt)
			if lhs.Cmp(rhs) > 0 {
				t.Fatalf("point (%d,%d) deviates from segment %v->%v by %s/%s > E=%d",
					p.T, p.V, a, b, diff.String(), dt.String(), e)
			}
		}
	}
}

// run 收集输入、输出并完成朴素对照校验，返回存档点。
func run(t *testing.T, e int64, pts ...Sample) []Sample {
	t.Helper()
	var log bytes.Buffer
	c, err := NewWithLogger(e, &log)
	if err != nil {
		t.Fatalf("New(%d): %v", e, err)
	}
	mustWrite(t, c, pts...)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := c.Archives()
	t.Logf("E=%d input=%v", e, pts)
	t.Logf("archives=%v", got)
	t.Logf("decision log:\n%s", log.String())
	naiveVerify(t, e, pts, got)
	return got
}
