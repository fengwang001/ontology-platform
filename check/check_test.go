package check

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	for _, want := range []int64{0, 1, 42, 1<<31 - 1, 1<<62 + 7} {
		st, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Write(want); err != nil {
			t.Fatalf("Write(%d): %v", want, err)
		}
		got, err := st.Read()
		if err != nil || got != want {
			t.Fatalf("Write(%d) 后 Read = %d, %v", want, got, err)
		}
	}
}

func TestCorruptCheckpoint(t *testing.T) {
	neg := make([]byte, 8)
	binary.BigEndian.PutUint64(neg, uint64(0xFFFFFFFFFFFFFFFF)) // -1，非法值
	cases := map[string][]byte{
		"空文件":     {},
		"3 字节":    []byte("bad"),
		"9 字节":    []byte("123456789"),
		"负值 8 字节": neg,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			st, _ := Open(dir)
			if err := st.Write(7); err != nil { // 先写一个合法位点
				t.Fatal(err)
			}
			if _, err := st.Read(); err != nil { // 成功读一次，lastRead=1
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "checkpoint"), content, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := st.Read(); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("损坏内容 %q 未报 ErrCorrupt: %v", content, err)
			}
			if st.lastRead != 1 { // 失败不留痕：计数器仍是上次成功读取的值
				t.Fatalf("失败读取改变了状态 lastRead=%d", st.lastRead)
			}
		})
	}
}

// TestRecoverRecordCountConstant 证明位点是单个 int64 标量：
// 分配 m 个编号后 Recover 读取的记录条数恒为 1，不随 m 线性增长。
func TestRecoverRecordCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run("m="+strconv.Itoa(m), func(t *testing.T) {
			st, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < m; i++ {
				if err := st.Write(int64(i + 1)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := st.Read()
			if err != nil || got != int64(m) {
				t.Fatalf("m=%d: Read = %d, %v", m, got, err)
			}
			if st.lastRead != 1 {
				t.Fatalf("m=%d: 读取记录条数 %d，应恒为 1", m, st.lastRead)
			}
		})
	}
}
