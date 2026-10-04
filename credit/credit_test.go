package credit

import "testing"

func TestBannedWindowBoundariesAndTouched(t *testing.T) {
	// K=2, W=10000；记录 610 与 5000。
	s := New(2, 10000)
	d := []byte("D")
	s.Add(d, 610)
	s.Add(d, 5000)
	if s.Banned(10609, d) != true {
		t.Fatal("now=10609 两条都在窗口内（610 > 609），应禁约")
	}
	if s.Banned(10610, d) != false {
		t.Fatal("now=10610 时 610 恰等 now-W=610，出窗口，不应禁约")
	}
	// 判定为真时最多读 K 条；判定为假时读到窗口外即停（这里读 2 条）。
	if n := s.Touched(); n != 2 {
		t.Fatalf("Banned 读取条数应为 2, got %d", n)
	}
}

func TestBannedTouchedCappedAtK(t *testing.T) {
	s := New(3, 1_000_000)
	p := []byte("p")
	for i := 0; i < 100; i++ {
		s.Add(p, int64(i))
	}
	if !s.Banned(100, p) {
		t.Fatal("窗口内 100 条，应禁约")
	}
	if n := s.Touched(); n != 3 {
		t.Fatalf("禁约判定读取条数不得超过 K=3, got %d", n)
	}
}

func TestBannedEmptyPatient(t *testing.T) {
	s := New(1, 10)
	if s.Banned(5, []byte("x")) {
		t.Fatal("无记录患者不应被禁约")
	}
	if s.Touched() != 0 {
		t.Fatal("无记录时读取条数应为 0")
	}
}
