package layout_test

import (
	"errors"
	"fmt"

	"ontology/layout"
)

func bytesN(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('v')
	}
	return b
}

// 题目给出的样例：内联属性被数据挤出、Clone 共享、Resize 回收外部块。
func Example_specWalkthrough() {
	m := layout.New(16, 64, 8, 10)
	f1, _ := m.Create()
	must(m.SetXattr(f1, []byte("a"), bytesN(5))) // 4+4+8=16 恰放得下
	s1, _ := m.Stat(f1)
	fmt.Println(s1.Mode == layout.Inline, s1.Xattrs[0].Location == layout.InInode, m.PoolUsed())

	must(m.Resize(f1, 1)) // 剩余 15，属性外置，仍是内联
	s1, _ = m.Stat(f1)
	fmt.Println(s1.Mode == layout.Inline, s1.Xattrs[0].Location == layout.External, s1.ExtID, m.PoolUsed())

	f2, _ := m.Clone(f1) // Ext 相同，共享
	s2, _ := m.Stat(f2)
	fmt.Println(s2.ExtID, m.PoolUsed())

	must(m.Resize(f2, 0)) // 文件 2 属性回 inode；文件 1 仍占外部块
	fmt.Println(m.PoolUsed())

	must(m.Resize(f1, 0)) // 最后一个使用者释放
	fmt.Println(m.PoolUsed())
	// Output:
	// true true 0
	// true true 1 1
	// 1 1
	// 1
	// 0
}

// P=1：独占第二个外部块被拒绝；Clone 因共享而成功。
func Example_poolSharing() {
	m := layout.New(16, 64, 8, 1)
	f1, _ := m.Create()
	must(m.Resize(f1, 1))
	must(m.SetXattr(f1, []byte("a"), bytesN(5))) // 外置，占 1

	f3, _ := m.Create()
	err := m.SetXattr(f3, []byte("a"), bytesN(9)) // 20 字节，必然外置且 Ext 不同
	fmt.Println(errors.Is(err, layout.ErrPoolFull))
	s3, _ := m.Stat(f3)
	fmt.Println(len(s3.Xattrs)) // 被整体拒绝，未留下属性

	f2, _ := m.Clone(f1) // 共享，占用不增
	s2, _ := m.Stat(f2)
	fmt.Println(f2, s2.ExtID, m.PoolUsed())
	// Output:
	// true
	// 0
	// 3 1 1
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
