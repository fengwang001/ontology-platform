package segment

import (
	"math/rand"
	"testing"
)

// 构造一个包含两种编码、含空值的多行组段用于损坏测试。
func corruptTestSeg(t *testing.T) *Segment {
	t.Helper()
	rng := rand.New(rand.NewSource(3))
	b := NewBuilder(Options{RowGroupRows: 17})
	dictVals := []int64{-1 << 50, 7, 1 << 50}
	for i := 0; i < 17*6; i++ {
		switch rng.Intn(8) {
		case 0:
			if err := b.AddNull(); err != nil {
				t.Fatal(err)
			}
		default:
			var v int64
			if i/17%2 == 0 {
				v = dictVals[rng.Intn(3)]
			} else {
				v = int64(rng.Intn(2000) - 1000)
			}
			if err := b.Add(v); err != nil {
				t.Fatal(err)
			}
		}
	}
	s, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTruncationSweep(t *testing.T) {
	s := corruptTestSeg(t)
	full := len(s.Bytes())
	for cut := 0; cut <= full; cut++ {
		prefix := s.Truncated(cut)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("cut=%d panicked: %v", cut, r)
				}
			}()
			s2, err := Open(prefix)
			if cut < full && err == nil {
				// 截断后 Open 成功则必须在访问时暴露损坏。
				broken := false
				for g := 0; g < s2.GroupCount(); g++ {
					if _, err := s2.GroupStats(g); err != nil {
						assertCorrupt(t, cut, err)
						broken = true
						break
					}
					if _, err := s2.DecodeGroup(g); err != nil {
						assertCorrupt(t, cut, err)
						broken = true
						break
					}
				}
				if !broken {
					t.Fatalf("cut=%d: truncation undetected", cut)
				}
				return
			}
			if err != nil {
				assertCorrupt(t, cut, err)
				return
			}
			// cut == full：完整段必须可用。
			if s2.RowCount() != s.RowCount() || s2.GroupCount() != s.GroupCount() {
				t.Fatalf("full segment mismatch")
			}
			for g := 0; g < s2.GroupCount(); g++ {
				if _, err := s2.DecodeGroup(g); err != nil {
					t.Fatalf("full segment group %d: %v", g, err)
				}
			}
		}()
	}
}

func assertCorrupt(t *testing.T, cut int, err error) {
	t.Helper()
	if !IsCorrupt(err) {
		t.Fatalf("cut=%d: err=%v is not a CorruptError", cut, err)
	}
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("cut=%d: cannot extract CorruptError from %v", cut, err)
	}
	if ce.Stage < StageHeader || ce.Stage > StageData {
		t.Fatalf("cut=%d: bad stage %v", cut, ce.Stage)
	}
}

func TestCorruptErrorStages(t *testing.T) {
	s := corruptTestSeg(t)
	// 段头截断：Group 必须为 -1，阶段为 header。
	if _, err := Open(s.Truncated(2)); err == nil {
		t.Fatal("expected error")
	} else {
		ce := err.(*CorruptError)
		if ce.Group != -1 || ce.Stage != StageHeader {
			t.Fatalf("got %+v", ce)
		}
	}
	// 数据区截断：错误必须带具体行组号。
	full := len(s.Bytes())
	_, err := Open(s.Truncated(full - 1))
	if err == nil {
		s2, _ := Open(s.Truncated(full - 1))
		for g := 0; g < s2.GroupCount(); g++ {
			if _, err = s2.DecodeGroup(g); err != nil {
				break
			}
		}
	}
	if err == nil {
		t.Fatal("expected corruption to be detected")
	}
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("err=%v not corrupt", err)
	}
	if ce.Group < -1 {
		t.Fatalf("bad group in %+v", ce)
	}
}

func TestNoPartialResults(t *testing.T) {
	s := corruptTestSeg(t)
	full := len(s.Bytes())
	for cut := 0; cut < full; cut++ {
		s2, err := Open(s.Truncated(cut))
		if err != nil {
			continue
		}
		for g := 0; g < s2.GroupCount(); g++ {
			rows, err := s2.DecodeGroup(g)
			if err != nil && rows != nil {
				t.Fatalf("cut=%d group=%d: partial result returned", cut, g)
			}
		}
	}
}
