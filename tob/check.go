package tob

import "errors"

// SelfCheck 在独立的临时定序器上执行第三节内置的十步操作序列，
// 核验四条不变量（含乱序补发、拒绝不留痕、大 m 下 O(1) 投递）；
// 不改变接收者自身状态。
func (l *Log) SelfCheck() error {
	c := New()

	propose := func(payload string, d, n int) error {
		s, err := c.Propose(payload)
		if err != nil {
			return err
		}
		if s != n-1 || c.Delivered() != d || c.nextSeq() != n {
			return errors.New("selfcheck: propose state mismatch")
		}
		return nil
	}
	deliver := func(want string, d, n int) error {
		s, p, ok := c.Deliver()
		if !ok || p != want || s != d || c.Delivered() != d || c.nextSeq() != n || c.scanCount() != 1 {
			return errors.New("selfcheck: deliver state mismatch")
		}
		return nil
	}

	// 1-7：A B D(A) C D(B) D E。
	if err := propose("A", 0, 2); err != nil {
		return err
	}
	if err := propose("B", 0, 3); err != nil {
		return err
	}
	if err := deliver("A", 1, 3); err != nil {
		return err
	}
	if err := propose("C", 1, 4); err != nil {
		return err
	}
	if err := deliver("B", 2, 4); err != nil {
		return err
	}
	if err := propose("D", 2, 5); err != nil {
		return err
	}
	if err := propose("E", 2, 6); err != nil {
		return err
	}

	// 8. 崩溃：游标与 nextSeq 保留，空洞 {3,4,5}；空洞未补时 Deliver 为空。
	c.Crash()
	if c.Delivered() != 2 || c.nextSeq() != 6 {
		return errors.New("selfcheck: crash must retain d and nextSeq")
	}
	if _, _, ok := c.Deliver(); ok || c.scanCount() != 0 {
		return errors.New("selfcheck: deliver into unfilled gap must be empty")
	}

	// 9. 乱序补发 (4,D)(3,C)(5,E)，沿用原 seq，nextSeq 不变。
	for _, rp := range []struct {
		s   int
		pay string
	}{{4, "D"}, {3, "C"}, {5, "E"}} {
		if err := c.RePropose(rp.s, rp.pay); err != nil {
			return err
		}
	}
	if c.nextSeq() != 6 {
		return errors.New("selfcheck: repropose must not allocate new seq")
	}

	// 10. 三次投递必按 seq 3,4,5 → C,D,E，共 5 条。
	for _, want := range []string{"C", "D", "E"} {
		if err := deliver(want, c.Delivered()+1, 6); err != nil {
			return err
		}
	}
	if c.Delivered() != 5 {
		return errors.New("selfcheck: final delivered must be 5")
	}

	// 不变量 4：空 payload 错误；三类 RePropose 错误用带空洞的独立场景，互不相同且不留痕。
	if _, err := c.Propose(""); err != ErrEmptyPayload {
		return errors.New("selfcheck: want ErrEmptyPayload")
	}
	g := New()
	g.Propose("m")
	g.Deliver() // d=1
	g.Propose("z")
	g.Crash() // d=1, n=3，槽位 2 空
	if err := g.RePropose(2, "z2"); err != nil {
		return err
	}
	rejects := []struct {
		s    int
		want error
	}{
		{1, ErrAlreadyDelivered}, // s ≤ d
		{3, ErrSeqOutOfRange},    // s ≥ n
		{2, ErrSlotFilled},       // 空洞内槽位已填
	}
	for _, r := range rejects {
		d, n, ln := g.Delivered(), g.nextSeq(), len(g.entries)
		if err := g.RePropose(r.s, "X"); err != r.want {
			return errors.New("selfcheck: wrong sentinel error")
		}
		if g.Delivered() != d || g.nextSeq() != n || len(g.entries) != ln {
			return errors.New("selfcheck: rejected op must leave no trace")
		}
	}

	// 复杂度：多档 m 下，紧接 m 次 Propose 的首次 Deliver 按下标定位，扫过 1 条。
	for _, m := range []int{100, 1000, 10000} {
		big := New()
		for range m {
			if _, err := big.Propose("x"); err != nil {
				return err
			}
		}
		if s, _, ok := big.Deliver(); !ok || s != 1 || big.scanCount() != 1 {
			return errors.New("selfcheck: deliver must be O(1) index access")
		}
	}
	return nil
}
