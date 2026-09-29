package slottedpage

import (
	"bytes"
	"sync"
	"testing"
)

// assertInvariant 用逐项累计独立核对页内字节账目与布局自洽。
func assertInvariant(t *testing.T, p *Page) {
	t.Helper()
	s := p.Stats()
	dirEnd := p.headerSize + p.slotCount*p.slotSize

	var sumRecords, live, minOff int
	minOff = p.pageSize
	seen := make(map[int]bool)
	for i := 0; i < p.slotCount; i++ {
		l := p.slotLength(i)
		o := p.slotOffset(i)
		if l == 0 {
			if o != 0 {
				t.Fatalf("空槽 %d 的 offset 必须为 0, got %d", i, o)
			}
			continue
		}
		live++
		sumRecords += l
		if o < minOff {
			minOff = o
		}
		if o < dirEnd || o+l > p.pageSize {
			t.Fatalf("记录 %d 越界: off=%d len=%d dirEnd=%d page=%d", i, o, l, dirEnd, p.pageSize)
		}
		for k := o; k < o+l; k++ {
			if seen[k] {
				t.Fatalf("字节 %d 被两条记录同时覆盖", k)
			}
			seen[k] = true
		}
	}
	if live == 0 {
		minOff = p.pageSize
	}
	if sumRecords != s.RecordBytes || sumRecords != p.recordBytes {
		t.Fatalf("记录字节账目不符: 逐项=%d stats=%d 内部=%d", sumRecords, s.RecordBytes, p.recordBytes)
	}
	if live != s.LiveRecords {
		t.Fatalf("存活记录数不符: 逐项=%d stats=%d", live, s.LiveRecords)
	}
	wantFree := p.pageSize - p.headerSize - p.slotCount*p.slotSize - sumRecords
	if wantFree != s.FreeBytes {
		t.Fatalf("可用字节不符: 公式=%d stats=%d", wantFree, s.FreeBytes)
	}
	if minOff != p.freeEnd || minOff != s.FreeEnd {
		t.Fatalf("freeEnd 不符: 扫描=%d 内部=%d stats=%d", minOff, p.freeEnd, s.FreeEnd)
	}
	wantContig := minOff - dirEnd
	if wantContig != s.ContigFree || wantContig < 0 {
		t.Fatalf("连续空闲区不符: 计算=%d stats=%d", wantContig, s.ContigFree)
	}
	if s.FreeBytes < s.ContigFree {
		t.Fatalf("可用字节 %d 不应小于连续空闲区 %d", s.FreeBytes, s.ContigFree)
	}
}

func rec(n int, b byte) []byte {
	d := make([]byte, n)
	for i := range d {
		d[i] = b
	}
	return d
}

func TestInsertGetAccounting(t *testing.T) {
	p, err := New(Config{PageSize: 128, HeaderSize: 32, SlotSize: 8}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Insert(rec(10, 'A'))
	t.Logf("输入=Insert(len=10) 输出=(id=%d,err=%v) 判定=空页直接放入页尾前方", id, err)
	if err != nil || id != 0 {
		t.Fatalf("want id 0, got %d %v", id, err)
	}
	got, err := p.Get(0)
	t.Logf("输入=Get(0) 输出=(len=%d,err=%v) 判定=按槽项定位拷贝", len(got), err)
	if err != nil || !bytes.Equal(got, rec(10, 'A')) {
		t.Fatalf("记录内容不符: %q err=%v", got, err)
	}
	s := p.Stats()
	if s.FreeBytes != 128-32-8-10 || s.ContigFree != 128-32-8-10 {
		t.Fatalf("初始账目错误: %+v", s)
	}
	assertInvariant(t, p)
}

func TestDeleteMiddleThenBigInsertTriggersCompaction(t *testing.T) {
	// 页 160B、页头 28B、槽 8B：扣除 3 个槽后内容容量 108B。
	p, _ := New(Config{PageSize: 160, HeaderSize: 28, SlotSize: 8}, nil)

	idA, _ := p.Insert(rec(30, 'A'))
	idB, _ := p.Insert(rec(30, 'B'))
	idC, _ := p.Insert(rec(30, 'C'))
	t.Logf("输入=三条Insert(30B) 输出=ids=%d,%d,%d 判定=尾向紧挨", idA, idB, idC)

	if err := p.Delete(1); err != nil {
		t.Fatal(err)
	}
	t.Logf("输入=Delete(1) 输出=nil 判定=中间槽置空不收回, 记录区留30B空洞")
	mid := p.Stats()
	if mid.SlotCount != 3 || mid.LiveRecords != 2 || mid.ContigFree != 18 || mid.FreeBytes != 48 {
		t.Fatalf("删中间后账目错误: %+v", mid)
	}
	assertInvariant(t, p)

	// 40B：连续区 18B 不够，可用 48B 足够 -> 复用最小空槽并整理。
	bigID, err := p.Insert(rec(40, 'D'))
	t.Logf("输入=Insert(len=40) 输出=(id=%d,err=%v) 判定=连续18<40且可用48>=40,整理一次", bigID, err)
	if err != nil || bigID != 1 {
		t.Fatalf("want reuse id 1 via compaction, got %d %v", bigID, err)
	}
	after := p.Stats()
	if after.Compactions != 1 {
		t.Fatalf("应恰好整理 1 次, got %d", after.Compactions)
	}
	if after.ContigFree != after.FreeBytes {
		t.Fatalf("整理后不应有碎片: contig=%d free=%d", after.ContigFree, after.FreeBytes)
	}
	for i, want := range []byte{'A', 'D', 'C'} {
		g, e := p.Get(i)
		if e != nil || (i == 1 && len(g) != 40) || (i != 1 && len(g) != 30) || g[0] != want {
			t.Fatalf("id %d 内容错误: %q err=%v", i, g, e)
		}
	}

	// 整理后编号越小越靠页尾（offset 越大），且彼此紧挨。
	o0, o1, o2 := p.slotOffset(0), p.slotOffset(1), p.slotOffset(2)
	l0, l1, l2 := p.slotLength(0), p.slotLength(1), p.slotLength(2)
	if !(o0 > o1 && o1 > o2) {
		t.Fatalf("整理后顺序错误: offs=%d,%d,%d", o0, o1, o2)
	}
	if o0+l0 != p.pageSize || o1+l1 != o0 || o2+l2 != o1 {
		t.Fatalf("整理后未紧挨: offs=%d,%d,%d lens=%d,%d,%d", o0, o1, o2, l0, l1, l2)
	}
	assertInvariant(t, p)
}

func TestTrailingEmptySlotsReclaimed(t *testing.T) {
	p, _ := New(Config{PageSize: 128, HeaderSize: 28, SlotSize: 8}, nil)
	for i := 0; i < 4; i++ {
		if _, err := p.Insert(rec(5, byte('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Delete(3); err != nil {
		t.Fatal(err)
	}
	t.Logf("输入=Delete(3) 输出=nil 判定=末尾空槽直接收回, SlotCount 4->3")
	if s := p.Stats(); s.SlotCount != 3 {
		t.Fatalf("末尾槽未收回: %+v", s)
	}

	if err := p.Delete(1); err != nil {
		t.Fatal(err)
	}
	if s := p.Stats(); s.SlotCount != 3 {
		t.Fatalf("删中间不应收回槽: %+v", s)
	}
	if err := p.Delete(2); err != nil {
		t.Fatal(err)
	}
	t.Logf("输入=Delete(2) 输出=nil 判定=1,2在末尾连续为空, 一并收回到槽0")
	if s := p.Stats(); s.SlotCount != 1 || s.LiveRecords != 1 {
		t.Fatalf("连带收回错误: %+v", s)
	}

	if err := p.Delete(0); err != nil {
		t.Fatal(err)
	}
	s := p.Stats()
	if s.SlotCount != 0 || s.FreeBytes != 128-28 || s.FreeEnd != 128 {
		t.Fatalf("清空后状态错误: %+v", s)
	}
	assertInvariant(t, p)
}

func TestReuseSmallestEmptySlot(t *testing.T) {
	p, _ := New(Config{PageSize: 128, HeaderSize: 28, SlotSize: 8}, nil)
	for i := 0; i < 4; i++ {
		if _, err := p.Insert(rec(4, byte('A'+i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Delete(1); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete(3); err != nil {
		t.Fatal(err)
	}
	id, err := p.Insert(rec(6, 'X'))
	t.Logf("输入=Insert(len=6) 输出=(id=%d,err=%v) 判定=空槽仅{1},复用最小编号1", id, err)
	if err != nil || id != 1 {
		t.Fatalf("want reuse 1, got %d %v", id, err)
	}
	id2, err := p.Insert(rec(6, 'Y'))
	t.Logf("输入=Insert(len=6) 输出=(id=%d,err=%v) 判定=无空槽,新增槽项得3", id2, err)
	if err != nil || id2 != 3 {
		t.Fatalf("want new id 3, got %d %v", id2, err)
	}
	assertInvariant(t, p)
}

func TestExactFillAndOneByteShort(t *testing.T) {
	p, _ := New(Config{PageSize: 128, HeaderSize: 28, SlotSize: 8}, nil)
	capacity := 128 - 28 // 空页中“1 槽 + 1 记录”总预算 = 100
	id, err := p.Insert(rec(capacity-8, 'F'))
	t.Logf("输入=Insert(len=%d) 输出=(id=%d,err=%v) 判定=8槽+92记录=100恰好填满", capacity-8, id, err)
	if err != nil {
		t.Fatal(err)
	}
	s := p.Stats()
	if s.FreeBytes != 0 || s.ContigFree != 0 {
		t.Fatalf("应恰好填满: %+v", s)
	}

	_, err = p.Insert([]byte{'Z'})
	t.Logf("输入=Insert(len=1) 输出=err=%v 判定=已满页再插需额外槽,空间不足", err)
	if err != ErrInsufficientSpace {
		t.Fatalf("want ErrInsufficientSpace, got %v", err)
	}
	assertInvariant(t, p)

	// 超容量一字节：记录本身 93B 合法，但连同一个槽项超出空页容量。
	q, _ := New(Config{PageSize: 128, HeaderSize: 28, SlotSize: 8}, nil)
	_, err = q.Insert(rec(capacity-8+1, 'G'))
	t.Logf("输入=Insert(len=%d) 输出=err=%v 判定=超空页容量1字节,判为过大", capacity-8+1, err)
	if err != ErrRecordTooLarge {
		t.Fatalf("want ErrRecordTooLarge, got %v", err)
	}
	if st := q.Stats(); st.SlotCount != 0 || st.FreeBytes != capacity {
		t.Fatalf("被拒绝操作不得改页: %+v", st)
	}
}

func TestRejectPrecedenceAndNoByteChange(t *testing.T) {
	p, _ := New(Config{PageSize: 80, HeaderSize: 28, SlotSize: 8}, nil)
	if _, err := p.Insert(rec(40, 'A')); err != nil {
		t.Fatal(err)
	}

	// 另造一页，含一个指向空槽的中间 id=1（保留其后的槽2以阻止末尾回收）。
	pe, _ := New(Config{PageSize: 120, HeaderSize: 28, SlotSize: 8}, nil)
	pe.Insert(rec(10, 'a'))
	pe.Insert(rec(10, 'b'))
	pe.Insert(rec(10, 'c'))
	pe.Delete(1)

	// 另造一页：页 120，三条 40B 后删中间槽；更新非最低槽 2 时，
	// 可用仅 24B 且释放的 40B 为碎片，更新到 52B 整理后也放不下。
	pSpace, _ := New(Config{PageSize: 200, HeaderSize: 28, SlotSize: 8}, nil)
	if _, err := pSpace.Insert(rec(40, 'A')); err != nil {
		t.Fatal(err)
	}
	if _, err := pSpace.Insert(rec(40, 'B')); err != nil {
		t.Fatal(err)
	}
	if _, err := pSpace.Insert(rec(40, 'C')); err != nil {
		t.Fatal(err)
	}
	pSpace.Delete(1)

	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"更新空记录优先于一切", func() error { return p.Update(0, nil) }, ErrEmptyRecord},
		{"插入空记录优先于过大", func() error { _, e := p.Insert(nil); return e }, ErrEmptyRecord},
		{"过大优先于编号越界", func() error { return p.Update(99, rec(60, 'B')) }, ErrRecordTooLarge},
		{"过大优先于指向空槽", func() error { return pe.Update(1, rec(100, 'B')) }, ErrRecordTooLarge},
		{"越界优先于空槽", func() error { return p.Update(5, rec(2, 'B')) }, ErrInvalidSlotID},
		{"指向空槽报错", func() error { return pe.Update(1, rec(2, 'D')) }, ErrEmptySlot},
		{"空间不足", func() error { return pSpace.Update(2, rec(120, 'B')) }, ErrInsufficientSpace},
	}
	for _, c := range cases {
		target := p
		if c.name == "空间不足" {
			target = pSpace
		} else if c.name == "过大优先于指向空槽" || c.name == "指向空槽报错" {
			target = pe
		}
		before := target.Marshal()
		err := c.fn()
		after := target.Marshal()
		t.Logf("用例=%s 输出=%v 判定=%v 页字节改变=%v", c.name, err, c.want, !bytes.Equal(before, after))
		if err != c.want {
			t.Fatalf("%s: want %v, got %v", c.name, c.want, err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("%s: 被拒绝操作改变了页字节", c.name)
		}
		assertInvariant(t, target)
	}
}

func TestUpdateFailureLeavesPageUntouched(t *testing.T) {
	// 制造碎片：删中间记录后，更新一条非最低记录为更大的值。
	p, _ := New(Config{PageSize: 160, HeaderSize: 28, SlotSize: 8}, nil)
	p.Insert(rec(30, 'A'))
	p.Insert(rec(30, 'B'))
	p.Insert(rec(30, 'C'))
	p.Delete(1)

	before := p.Marshal()
	// 可用字节为 48（=160-28-24-60）：更新到 50B 可整理成功，到 49 以上的上限验证边界。
	if err := p.Update(2, rec(48, 'X')); err != nil {
		t.Fatalf("48B 在可用 48B 内应整理成功, got %v", err)
	}
	assertInvariant(t, p)

	// 再来一次：当前记录 48B，全页仅剩 30B 可用，更新到 31B 必须拒绝且原样。
	before = p.Marshal()
	err := p.Update(0, rec(79, 'Y')) // 需 79B，远超可用量
	t.Logf("输入=Update(0,len=79) 输出=err=%v 判定=可用字节不足,整理也放不下,拒绝", err)
	if err != ErrInsufficientSpace {
		t.Fatalf("want ErrInsufficientSpace, got %v", err)
	}
	if after := p.Marshal(); !bytes.Equal(before, after) {
		t.Fatalf("失败的更新改变了页字节")
	}

	// 更新缩短/原地扩展不应整理；整理成功后内容与编号正确。
	p2, _ := New(Config{PageSize: 160, HeaderSize: 28, SlotSize: 8}, nil)
	p2.Insert(rec(30, 'A'))
	p2.Insert(rec(30, 'B'))
	p2.Delete(0)
	if err := p2.Update(1, rec(40, 'Y')); err != nil {
		t.Fatal(err)
	}
	if c := p2.compactions; c != 0 {
		t.Fatalf("最低记录原地扩展不应整理, got %d", c)
	}
	g, _ := p2.Get(1)
	if !bytes.Equal(g, rec(40, 'Y')) {
		t.Fatalf("扩展后内容错误: %q", g)
	}
	if err := p2.Update(1, rec(12, 'Z')); err != nil {
		t.Fatal(err)
	}
	g, _ = p2.Get(1)
	if !bytes.Equal(g, rec(12, 'Z')) {
		t.Fatalf("缩短后内容错误: %q", g)
	}
	assertInvariant(t, p)
	assertInvariant(t, p2)
}

func TestNoCompactionWhenContigEnough(t *testing.T) {
	p, _ := New(Config{PageSize: 160, HeaderSize: 28, SlotSize: 8}, nil)
	p.Insert(rec(20, 'A'))
	p.Insert(rec(20, 'B'))
	p.Insert(rec(20, 'C'))
	p.Delete(1) // 删中间 B，保留槽 0/2；目录不收缩
	if c := p.compactions; c != 0 {
		t.Fatalf("删除不应整理, got %d", c)
	}
	id, err := p.Insert(rec(10, 'D')) // 连续区 48B，足够，绝不整理
	t.Logf("输入=Insert(len=10) 输出=(id=%d,err=%v) 判定=连续区足够,绝不整理", id, err)
	if err != nil || id != 1 || p.compactions != 0 {
		t.Fatalf("连续区足够时不得整理: id=%d err=%v compactions=%d", id, err, p.compactions)
	}
	assertInvariant(t, p)
}

func TestImageDeterminismAndRoundTrip(t *testing.T) {
	cfg := Config{PageSize: 200, HeaderSize: 32, SlotSize: 12}
	build := func() *Page {
		q, _ := New(cfg, nil)
		q.Insert(rec(15, 'A'))
		q.Insert(rec(25, 'B'))
		q.Insert(rec(7, 'C'))
		q.Delete(1)
		q.Insert(rec(33, 'D')) // 触发整理
		q.Insert(rec(3, 'E'))
		return q
	}

	p1, p2 := build(), build()
	im1, im2 := p1.Marshal(), p2.Marshal()
	t.Logf("输入=相同操作序列 输出=映像(%d字节) 判定=逐字节相同=%v", len(im1), bytes.Equal(im1, im2))
	if !bytes.Equal(im1, im2) {
		t.Fatalf("相同操作序列产生了不同页映像")
	}

	restored, err := New(cfg, im1)
	if err != nil {
		t.Fatalf("还原映像失败: %v", err)
	}
	if im3 := restored.Marshal(); !bytes.Equal(im1, im3) {
		t.Fatalf("还原页的映像与原映像不一致")
	}
	for id, wantLen := range map[int]int{0: 15, 1: 33, 2: 7, 3: 3} {
		g, e := restored.Get(id)
		if e != nil || len(g) != wantLen {
			t.Fatalf("还原后 id=%d 错误: len=%d err=%v", id, len(g), e)
		}
	}

	// 还原后继续执行同一序列仍得到相同映像。
	for _, q := range []*Page{p1, restored} {
		q.Update(0, rec(18, 'F'))
		q.Delete(3)
	}
	if a, b := p1.Marshal(), restored.Marshal(); !bytes.Equal(a, b) {
		t.Fatalf("还原页继续操作后与原页不一致")
	}

	// 损坏的映像必须被拒绝。
	bad := append([]byte(nil), im1...)
	bad[16] ^= 0xFF
	if _, err := New(cfg, bad); err == nil {
		t.Fatalf("损坏映像应被拒绝")
	}
	t.Logf("输入=篡改槽数的映像 输出=err!=nil 判定=校验失败拒绝还原")
	assertInvariant(t, restored)
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	p, _ := New(Config{PageSize: 512, HeaderSize: 28, SlotSize: 8}, nil)
	for i := 0; i < 4; i++ {
		p.Insert(rec(20, byte('A'+i)))
	}

	var readerWG, writerWG sync.WaitGroup
	stop := make(chan struct{})

	// 读者：拿到的记录必须始终是某条写入过的完整记录。
	valid := func(b []byte) bool {
		if len(b) == 0 || len(b) > 100 {
			return false
		}
		c := b[0]
		for _, x := range b {
			if x != c {
				return false
			}
		}
		return true
	}
	for r := 0; r < 8; r++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for id := 0; id < 8; id++ {
					g, err := p.Get(id)
					if err == nil && !valid(g) {
						t.Errorf("读者看到了整理中的半移动状态: %q", g)
						return
					}
				}
				_ = p.Stats()
			}
		}()
	}

	// 写者：反复“大记录制造碎片 -> 更大插入触发整理”。
	for w := 0; w < 4; w++ {
		writerWG.Add(1)
		go func(seed byte) {
			defer writerWG.Done()
			for round := 0; round < 60; round++ {
				ids := make([]int, 0, 4)
				for i := 0; i < 4; i++ {
					id, err := p.Insert(rec(40, seed+byte(i)))
					if err == nil {
						ids = append(ids, id)
					}
				}
				for _, id := range ids {
					if id%2 == 0 {
						_ = p.Delete(id)
					}
				}
				_, _ = p.Insert(rec(70, seed)) // 高概率触发整理
				for _, id := range ids {
					if id%2 != 0 {
						_ = p.Update(id, rec(20, seed))
					}
				}
				for _, id := range ids {
					if id%2 != 0 {
						_ = p.Delete(id)
					}
				}
			}
		}(byte('a' + w*4))
	}

	// 封送者：与读写并发，持续取映像并做轻量自洽检查。
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			im := p.Marshal()
			if len(im) != 512 {
				t.Errorf("并发期间得到非法长度映像: %d", len(im))
				return
			}
		}
	}()

	// 写者执行有限轮次；全部写完后再停止读者/封送者。
	writerWG.Wait()
	close(stop)
	readerWG.Wait()
	assertInvariant(t, p)
	t.Logf("输入=8读者/4写者/1封送并发 输出=最终槽数=%d 整理次数=%d 判定=无半移动状态、账目自洽",
		p.Stats().SlotCount, p.Stats().Compactions)
}
