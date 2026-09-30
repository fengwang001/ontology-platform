package slottedpage

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

const (
	testPage   = 256
	testHeader = 16
	testSlot   = 8
)

func rec(fill byte, n int) []byte {
	return bytes.Repeat([]byte{fill}, n)
}

func mustNew(t *testing.T) *Page {
	t.Helper()
	p, err := New(testPage, testHeader, testSlot)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func mustInsert(t *testing.T, p *Page, data []byte) int {
	t.Helper()
	id, err := p.Insert(data)
	if err != nil {
		t.Fatalf("Insert(%d bytes): %v", len(data), err)
	}
	return id
}

func mustGet(t *testing.T, p *Page, id int) []byte {
	t.Helper()
	got, err := p.Get(id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	return got
}

// 删中间记录后插大记录触发整理，整理前后编号稳定、内容不变。
func TestCompactionAfterMiddleDelete(t *testing.T) {
	p := mustNew(t)
	a, b, c := rec('A', 40), rec('B', 40), rec('C', 40)
	idA := mustInsert(t, p, a)
	idB := mustInsert(t, p, b)
	idC := mustInsert(t, p, c)
	t.Logf("输入: 插入 A/B/C 各 40 字节 -> 编号 %d/%d/%d", idA, idB, idC)

	if err := p.Delete(idB); err != nil {
		t.Fatalf("Delete(%d): %v", idB, err)
	}
	t.Logf("输入: 删除中间记录 B(编号 %d)，可用=%d 连续空闲=%d 整理次数=%d",
		idB, p.Available(), p.ContiguousFree(), p.Compactions())

	d := rec('D', 120)
	idD, err := p.Insert(d)
	if err != nil {
		t.Fatalf("Insert(D 120 字节): %v", err)
	}
	t.Logf("输出: 插入 D(120 字节) -> 编号 %d，整理次数=%d", idD, p.Compactions())

	if idD != idB {
		t.Errorf("判定依据: 插入优先复用编号最小的空槽，期望复用 B 的编号 %d，实际 %d", idB, idD)
	}
	if p.Compactions() != 1 {
		t.Errorf("判定依据: 连续空闲 96 < 120 而可用 216 >= 120，应整理恰好 1 次，实际 %d", p.Compactions())
	}
	for id, want := range map[int][]byte{idA: a, idC: c, idD: d} {
		if got := mustGet(t, p, id); !bytes.Equal(got, want) {
			t.Errorf("判定依据: 整理前后编号 %d 内容应稳定不变", id)
		}
	}
	if _, err := p.Get(3); !errors.Is(err, ErrInvalidRecordID) {
		t.Errorf("判定依据: 只有 3 个存活记录，编号 3 应越界，实际 err=%v", err)
	}
	t.Logf("判定通过: 整理后编号 0/1/2 内容不变，可用=%d 与逐项累计一致", p.Available())
}

// 连续空闲区足够时不得整理。
func TestNoCompactionWhenContiguousEnough(t *testing.T) {
	p := mustNew(t)
	mustInsert(t, p, rec('A', 40))
	mustInsert(t, p, rec('B', 40))
	idC := mustInsert(t, p, rec('C', 40))
	if err := p.Delete(idC); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	t.Logf("输入: 插入 A/B/C 各 40 字节后删除最低记录 C，连续空闲=%d", p.ContiguousFree())

	idD := mustInsert(t, p, rec('D', 30))
	t.Logf("输出: 插入 D(30 字节) -> 编号 %d，整理次数=%d", idD, p.Compactions())
	if p.Compactions() != 0 {
		t.Errorf("判定依据: 连续空闲区足够时不应整理，实际整理 %d 次", p.Compactions())
	}
}

// 删除末尾槽时连同其前方连续空槽一并收回。
func TestTrailingSlotReclaim(t *testing.T) {
	p := mustNew(t)
	for i := 0; i < 4; i++ {
		mustInsert(t, p, rec(byte('A'+i), 10))
	}
	t.Logf("输入: 插入 4 条记录，槽数=%d 可用=%d", p.SlotCount(), p.Available())

	if err := p.Delete(2); err != nil { // 中间槽置空，不收回
		t.Fatalf("Delete(2): %v", err)
	}
	if p.SlotCount() != 4 {
		t.Fatalf("判定依据: 删除中间槽 2 不触发收回，槽数应为 4，实际 %d", p.SlotCount())
	}
	if err := p.Delete(3); err != nil { // 末尾槽，连带槽 2 一并收回
		t.Fatalf("Delete(3): %v", err)
	}
	t.Logf("输出: 删除末尾槽 3 后槽数=%d 可用=%d", p.SlotCount(), p.Available())
	if p.SlotCount() != 2 {
		t.Errorf("判定依据: 末尾空槽 3 连同其前方连续空槽 2 一并收回，槽数应为 2，实际 %d", p.SlotCount())
	}
	wantAvail := testPage - testHeader - 2*testSlot - 20
	if p.Available() != wantAvail {
		t.Errorf("判定依据: 可用=页大小-页头-槽目录-存活记录=%d，实际 %d", wantAvail, p.Available())
	}
}

// 插入优先复用编号最小的空槽。
func TestReuseSmallestFreeSlot(t *testing.T) {
	p := mustNew(t)
	for i := 0; i < 4; i++ {
		mustInsert(t, p, rec(byte('A'+i), 10))
	}
	for _, id := range []int{2, 1} { // 先删 2 再删 1，空槽为 {1,2}
		if err := p.Delete(id); err != nil {
			t.Fatalf("Delete(%d): %v", id, err)
		}
	}
	t.Logf("输入: 4 条记录中删除编号 2 与 1，空槽集合 {1,2}")

	idE := mustInsert(t, p, rec('E', 10))
	idF := mustInsert(t, p, rec('F', 10))
	t.Logf("输出: 连续插入 E/F -> 编号 %d/%d", idE, idF)
	if idE != 1 || idF != 2 {
		t.Errorf("判定依据: 应按升序复用最小空槽，期望 1/2，实际 %d/%d", idE, idF)
	}
	idG := mustInsert(t, p, rec('G', 10))
	if idG != 4 {
		t.Errorf("判定依据: 无空槽才新增槽项，期望新编号 4，实际 %d", idG)
	}
}

// 恰好填满成功，差一字节拒绝。
func TestExactFillAndOneByteShort(t *testing.T) {
	p := mustNew(t)
	capacity := testPage - testHeader // 空页容量 240
	big := capacity - testSlot        // 记录+槽项恰好等于空页容量
	t.Logf("输入: 空页容量 %d，插入 %d 字节记录（连同一个槽项 %d 恰好填满）", capacity, big, big+testSlot)

	id := mustInsert(t, p, rec('X', big))
	if p.Available() != 0 {
		t.Fatalf("判定依据: 恰好填满后可用应为 0，实际 %d", p.Available())
	}
	before := p.Image()

	err := p.Update(id, rec('Y', big+1))
	t.Logf("输出: 变长更新到 %d 字节 -> err=%v", big+1, err)
	if !errors.Is(err, ErrRecordTooLarge) {
		t.Errorf("判定依据: 单条记录连同一个槽项超过空页容量，应报 ErrRecordTooLarge，实际 %v", err)
	}
	if !bytes.Equal(p.Image(), before) {
		t.Errorf("判定依据: 被拒绝的操作不得改变页的任何字节")
	}

	// 差一字节：可用空间不足但整体未超空页容量。
	q := mustNew(t)
	mustInsert(t, q, rec('A', 100))
	mustInsert(t, q, rec('B', 100))
	// 可用 = 256-16-16-200 = 24；无空槽，插入需连新槽一起记账。
	need := q.Available() - testSlot + 1 // 17 字节记录 + 8 槽 = 25 > 24
	_, err = q.Insert(rec('Z', need))
	t.Logf("输出: 可用=%d，插入 %d 字节（含新槽共需 %d）-> err=%v",
		q.Available(), need, need+testSlot, err)
	if !errors.Is(err, ErrInsufficientSpace) {
		t.Errorf("判定依据: 可用空间不足应报 ErrInsufficientSpace，实际 %v", err)
	}
	if _, err = q.Insert(rec('Z', need-1)); err != nil { // 16+8=24 恰好填满
		t.Errorf("判定依据: 少一字节恰好可放入，实际 err=%v", err)
	}
	if q.Available() != 0 {
		t.Errorf("判定依据: 恰好填满后可用应为 0，实际 %d", q.Available())
	}
}

// 变长更新失败后页保持原样。
func TestFailedUpdateLeavesPageUntouched(t *testing.T) {
	p := mustNew(t)
	idA := mustInsert(t, p, rec('A', 100))
	idB := mustInsert(t, p, rec('B', 100))
	t.Logf("输入: 插入 A/B 各 100 字节，可用=%d", p.Available())
	before := p.Image()

	err := p.Update(idB, rec('Z', 200))
	t.Logf("输出: 更新 B 到 200 字节 -> err=%v", err)
	if !errors.Is(err, ErrInsufficientSpace) {
		t.Errorf("判定依据: 增长 100 字节超过可用 %d，应报 ErrInsufficientSpace，实际 %v",
			256-16-16-200, err)
	}
	if !bytes.Equal(p.Image(), before) {
		t.Errorf("判定依据: 被拒绝的更新不得改变页的任何字节")
	}
	if got := mustGet(t, p, idA); !bytes.Equal(got, rec('A', 100)) {
		t.Errorf("判定依据: 更新失败后记录 A 应原样保留")
	}
	if got := mustGet(t, p, idB); !bytes.Equal(got, rec('B', 100)) {
		t.Errorf("判定依据: 更新失败后记录 B 应原样保留")
	}
}

// 多因并存时按 空记录 > 过大 > 编号非法 > 空间不足 的顺序只报第一个。
func TestErrorPrecedence(t *testing.T) {
	p := mustNew(t)
	mustInsert(t, p, rec('A', 10))
	before := p.Image()
	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"空记录+编号非法", func() error { return p.Update(99, nil) }, ErrEmptyRecord},
		{"过大+编号非法", func() error {
			return p.Update(99, rec('X', testPage))
		}, ErrRecordTooLarge},
		{"编号非法+空间不足", func() error { return p.Update(99, rec('X', 10)) }, ErrInvalidRecordID},
		{"插入空记录", func() error { _, err := p.Insert(nil); return err }, ErrEmptyRecord},
		{"插入过大记录", func() error {
			_, err := p.Insert(rec('X', testPage-testHeader-testSlot+1))
			return err
		}, ErrRecordTooLarge},
		{"删除越界编号", func() error { return p.Delete(7) }, ErrInvalidRecordID},
		{"删除空槽", func() error {
			q := mustNew(t)
			id := mustInsert(t, q, rec('A', 10))
			if err := q.Delete(id); err != nil {
				return err
			}
			return q.Delete(id)
		}, ErrInvalidRecordID},
	}
	for _, tc := range cases {
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Errorf("%s: 判定依据: 期望 %v，实际 %v", tc.name, tc.want, err)
		} else {
			t.Logf("%s -> err=%v（符合预期）", tc.name, err)
		}
	}
	if !bytes.Equal(p.Image(), before) {
		t.Errorf("判定依据: 全部被拒操作不得改变页的任何字节")
	}
}

// 对两个页施加同一操作序列，页映像逐字节相同。
func TestDeterministicImage(t *testing.T) {
	run := func() []byte {
		p := mustNew(t)
		ids := make([]int, 0, 8)
		for i := 0; i < 6; i++ {
			id, err := p.Insert(rec(byte('a'+i), 30))
			if err != nil {
				t.Fatalf("Insert: %v", err)
			}
			ids = append(ids, id)
		}
		if err := p.Delete(ids[1]); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if err := p.Delete(ids[3]); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := p.Insert(rec('Z', 60)); err != nil { // 连续空闲不足，触发整理
			t.Fatalf("Insert: %v", err)
		}
		if err := p.Update(ids[0], rec('Q', 40)); err != nil { // 再次触发整理
			t.Fatalf("Update: %v", err)
		}
		return p.Image()
	}
	img1, img2 := run(), run()
	t.Logf("输入: 同一操作序列执行两遍；输出: 映像长度 %d，整理场景已覆盖", len(img1))
	if !bytes.Equal(img1, img2) {
		t.Errorf("判定依据: 同一操作序列应得到逐字节相同的页映像")
	}
}

// 页映像可还原出完全相同的页。
func TestRestoreRoundTrip(t *testing.T) {
	p := mustNew(t)
	idA := mustInsert(t, p, rec('A', 50))
	idB := mustInsert(t, p, rec('B', 50))
	idC := mustInsert(t, p, rec('C', 50))
	if err := p.Delete(idB); err != nil { // 删中间记录制造碎片
		t.Fatalf("Delete: %v", err)
	}
	if _, err := p.Insert(rec('D', 100)); err != nil { // 连续空闲不足，触发整理
		t.Fatalf("Insert: %v", err)
	}
	if err := p.Update(idC, rec('c', 60)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	_ = idA
	img := p.Image()
	t.Logf("输入: 操作后页映像（槽数=%d 整理次数=%d 可用=%d）",
		p.SlotCount(), p.Compactions(), p.Available())

	q, err := Restore(img, testHeader, testSlot)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !bytes.Equal(q.Image(), img) {
		t.Errorf("判定依据: 还原后的页映像应与原映像逐字节相同")
	}
	if q.Compactions() != p.Compactions() || q.SlotCount() != p.SlotCount() ||
		q.Available() != p.Available() {
		t.Errorf("判定依据: 还原后槽数/整理次数/可用字节应与原页一致")
	}
	for id := 0; id < p.SlotCount(); id++ {
		gotP, errP := p.Get(id)
		gotQ, errQ := q.Get(id)
		if (errP != nil) != (errQ != nil) || !bytes.Equal(gotP, gotQ) {
			t.Errorf("判定依据: 还原后编号 %d 的记录应与原页一致", id)
		}
	}
	t.Logf("输出: 还原页校验通过，整理次数=%d", q.Compactions())
}

// 并发读写：读者不得看到整理中的半移动状态。
func TestConcurrentReadWrite(t *testing.T) {
	p, err := New(4096, testHeader, testSlot)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const slots = 16
	ids := make([]int, slots)
	for i := range ids {
		ids[i] = mustInsert(t, p, rec(byte('A'+i), 32))
	}
	t.Logf("输入: %d 条初始记录，4 读者 + 2 写者并发 2000 轮", slots)

	var writers, readers sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func(r int) {
			defer readers.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				id := ids[(i+r)%slots]
				got, err := p.Get(id)
				if err == nil && len(got) > 0 && !bytes.Equal(got, rec(got[0], len(got))) {
					t.Errorf("判定依据: 读者不应看到半移动状态，编号 %d 内容混杂", id)
					return
				}
				_ = p.Available()
				_ = p.Image()
			}
		}(r)
	}
	for w := 0; w < 2; w++ {
		writers.Add(1)
		go func(w int) {
			defer writers.Done()
			for i := 0; i < 2000; i++ {
				id := ids[(i+w)%slots]
				size := 16 + (i*7+w)%48
				if err := p.Update(id, rec(byte('a'+w), size)); err != nil &&
					!errors.Is(err, ErrInsufficientSpace) && !errors.Is(err, ErrInvalidRecordID) {
					t.Errorf("Update: 意外错误 %v", err)
					return
				}
				if i%97 == 0 {
					_ = p.Delete(id)
				}
				if i%89 == 0 {
					if _, err := p.Insert(rec(byte('A'+w), size)); err != nil &&
						!errors.Is(err, ErrInsufficientSpace) {
						t.Errorf("Insert: 意外错误 %v", err)
						return
					}
				}
			}
		}(w)
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	// 写者完成后做最终一致性校验：可用字节与逐项累计一致。
	sum := 0
	live := 0
	for id := 0; id < p.SlotCount(); id++ {
		if got, err := p.Get(id); err == nil {
			sum += len(got)
			live++
		}
	}
	want := 4096 - testHeader - p.SlotCount()*testSlot - sum
	t.Logf("输出: 存活记录 %d 条共 %d 字节，槽数=%d 整理次数=%d 可用=%d",
		live, sum, p.SlotCount(), p.Compactions(), p.Available())
	if p.Available() != want {
		t.Errorf("判定依据: 可用=页大小-页头-槽目录-存活记录=%d，实际 %d", want, p.Available())
	}
}

// 账目公式抽查：每步操作后可用字节都与逐项累计一致。
func TestAccountingInvariant(t *testing.T) {
	p := mustNew(t)
	var live []int // 下标即编号，0 表示空槽
	check := func(step string) {
		t.Helper()
		sum := 0
		for _, n := range live {
			sum += n
		}
		want := testPage - testHeader - p.SlotCount()*testSlot - sum
		if p.Available() != want {
			t.Fatalf("%s: 判定依据: 可用=页大小-页头-槽目录-存活记录=%d，实际 %d",
				step, want, p.Available())
		}
		t.Logf("%s -> 可用=%d（与逐项累计一致）", step, p.Available())
	}
	for len(live) < 5 {
		id := mustInsert(t, p, rec('A', 20))
		for len(live) <= id {
			live = append(live, 0)
		}
		live[id] = 20
		check(fmt.Sprintf("插入编号 %d(20 字节)", id))
	}
	if err := p.Delete(2); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	live[2] = 0
	check("删除编号 2")
	if err := p.Update(0, rec('B', 35)); err != nil {
		t.Fatalf("Update: %v", err)
	}
	live[0] = 35
	check("编号 0 更新为 35 字节")
}
