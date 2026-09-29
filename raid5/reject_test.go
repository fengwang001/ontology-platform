package raid5

import "testing"

// TestRejectReasons 覆盖四类必须整体拒绝且不得写任何盘的场景。
func TestRejectReasons(t *testing.T) {
	n, stripes := 4, 3
	setup := func(t *testing.T) (*Volume, []Disk) {
		disks := newMemDiskSet(n, stripes)
		v := reopen(t, disks, newMemJournalFile(), n, stripes)
		return v, disks
	}

	t.Run("块号越界", func(t *testing.T) {
		v, disks := setup(t)
		total := stripes * (n - 1)
		bad := [][]byte{mkBlock(1, 1)}
		cases := []struct {
			name string
			err  error
			fn   func() error
		}{
			{"起始块为负", ErrBlockOutOfRange, func() error { return v.Write(-1, bad) }},
			{"写过卷尾", ErrBlockOutOfRange, func() error { return v.Write(total, bad) }},
			{"空写", ErrBlockOutOfRange, func() error { return v.Write(0, nil) }},
			{"读越界", ErrBlockOutOfRange, func() error { return v.Read(total, bad) }},
		}
		for _, c := range cases {
			before := cloneDisks(disks)
			err := c.fn()
			expectReject(t, c.name, before, err, c.err, disks)
		}
	})

	t.Run("已有一盘失效再失效另一盘", func(t *testing.T) {
		v, disks := setup(t)
		if err := v.FailDisk(2); err != nil {
			t.Fatal(err)
		}
		before := cloneDisks(disks)
		err := v.FailDisk(0)
		expectReject(t, "二次失效", before, err, ErrTwoDisksFailed, disks)
	})

	t.Run("对未失效盘重建", func(t *testing.T) {
		v, disks := setup(t)
		before := cloneDisks(disks)
		fresh := &memDisk{index: 1, stripes: stripes, rebuilt: map[int]bool{},
			data: blankPlatter(stripes)}
		err := v.ReplaceDisk(1, fresh)
		expectReject(t, "健康盘重建", before, err, ErrRebuildHealthyDisk, disks)
	})

	t.Run("重建中再次重建", func(t *testing.T) {
		v, disks := setup(t)
		// 制造失效并立即换盘（后台重建很快，但用一个已在重建的卷更稳妥：
		// 换盘后立刻再次换盘应被拒绝）。
		if err := v.FailDisk(0); err != nil {
			t.Fatal(err)
		}
		gate := make(chan bool, 1)
		v.setRebuildGate(gate)
		fresh := newFreshMem(0, stripes)
		if err := v.ReplaceDisk(0, fresh); err != nil {
			t.Fatal(err)
		}
		// 不放任何令牌：重建 goroutine 停在第 0 条带前，状态为“进行中”。
		before := cloneDisks(disks)
		again := newFreshMem(0, stripes)
		err := v.ReplaceDisk(0, again)
		expectReject(t, "重建中再重建", before, err, ErrRebuildInProgress, disks)
		gate <- false // 中止后台重建
		v.RebuildDone()
	})

	t.Run("盘号越界", func(t *testing.T) {
		v, disks := setup(t)
		before := cloneDisks(disks)
		err := v.FailDisk(n)
		expectReject(t, "失效越界盘号", before, err, ErrDiskIndexOutOfRange, disks)
	})
}

func blankPlatter(stripes int) [][]byte {
	d := make([][]byte, stripes)
	for i := range d {
		d[i] = make([]byte, BlockSize)
	}
	return d
}

func newFreshMem(index, stripes int) *memDisk {
	return &memDisk{index: index, stripes: stripes, rebuilt: map[int]bool{},
		data: blankPlatter(stripes)}
}
