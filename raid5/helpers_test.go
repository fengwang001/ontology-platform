package raid5

import (
	"bytes"
	"fmt"
	"testing"
)

func mkBlock(seed byte, tag int) []byte {
	b := make([]byte, BlockSize)
	for i := range b {
		b[i] = seed ^ byte(i) ^ byte(tag)
	}
	return b
}

func zeroBlock() []byte { return make([]byte, BlockSize) }

// verifyStripeParity 校验条带 s 所有存活盘满足 parity == xor(data...)。
// 返回判定说明与是否一致。
func verifyStripeParity(t *testing.T, disks []Disk, n, s int) (string, bool) {
	t.Helper()
	parity, data := stripeMap(n, s)
	if disks[parity].Failed() {
		return fmt.Sprintf("stripe=%d 校验盘失效，跳过", s), true
	}
	p := make([]byte, BlockSize)
	if err := disks[parity].ReadBlockForRebuild(s, p); err != nil {
		return fmt.Sprintf("stripe=%d 读校验块失败: %v", s, err), false
	}
	acc := make([]byte, BlockSize)
	first := true
	for _, d := range data {
		if disks[d].Failed() {
			return fmt.Sprintf("stripe=%d 数据盘 %d 失效，跳过", s, d), true
		}
		buf := make([]byte, BlockSize)
		if err := disks[d].ReadBlockForRebuild(s, buf); err != nil {
			return fmt.Sprintf("stripe=%d 数据盘 %d 读失败(未重建): %v", s, d, err), false
		}
		if first {
			copy(acc, buf)
			first = false
		} else {
			xorBlocks(acc, buf)
		}
	}
	ok := bytes.Equal(p, acc)
	return fmt.Sprintf("stripe=%d parity==XOR(data): %v", s, ok), ok
}

func verifyAllStripes(t *testing.T, disks []Disk, n, stripes int) bool {
	t.Helper()
	allOK := true
	for s := 0; s < stripes; s++ {
		msg, ok := verifyStripeParity(t, disks, n, s)
		t.Logf("判定: %s", msg)
		if !ok {
			allOK = false
		}
	}
	return allOK
}

// reopen 用当前盘集合与保留的日志重新打开卷（模拟断电重启）。
func reopen(t *testing.T, disks []Disk, jf *memJournalFile, n, stripes int) *Volume {
	t.Helper()
	v, err := Create(disks, NewJournal(jf), Options{N: n, Stripes: stripes})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return v
}

func readAll(t *testing.T, v *Volume, n, stripes int) [][]byte {
	t.Helper()
	total := stripes * (n - 1)
	dst := make([][]byte, total)
	for i := range dst {
		dst[i] = make([]byte, BlockSize)
	}
	if err := v.Read(0, dst); err != nil {
		t.Fatalf("readAll: %v", err)
	}
	return dst
}

// expectReject 断言操作返回指定错误且未写盘（快照逐字节相同）。
func expectReject(t *testing.T, name string, before []Disk, err error, want error, after []Disk) {
	t.Helper()
	t.Logf("输入: 操作=%s 输出错误=%v 判定依据: errors.Is(=%v) 应为 %v",
		name, err, err == want, want)
	if err != want {
		t.Fatalf("%s: got %v want %v", name, err, want)
	}
	if !disksEqual(before, after) {
		t.Fatalf("%s: rejected operation modified disks", name)
	}
}

func disksEqual(a, b []Disk) bool {
	for i := range a {
		x := a[i].(*memDisk)
		y := b[i].(*memDisk)
		for s := range x.data {
			if !bytes.Equal(x.data[s], y.data[s]) {
				return false
			}
		}
	}
	return true
}
