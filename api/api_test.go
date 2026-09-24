package api_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"ontology/api"
	"ontology/check"
)

func fatalf(t *testing.T, bad bool, msg string, args ...any) { // bad 为真即判失败
	t.Helper()
	if bad {
		t.Fatalf(msg, args...)
	}
}

func posOf(dir string) int64 { st, _ := check.Open(dir); n, _ := st.Read(); return n }

func mustNew(t *testing.T, dir string) *api.Allocator {
	t.Helper()
	a, err := api.New(dir)
	fatalf(t, err != nil, "New: %v", err)
	return a
}

// TestMisc 三类哨兵错误可判定且互不相同；空 dir 报错；SelfCheck 通过。
func TestMisc(t *testing.T) {
	_, err := api.New("")
	fatalf(t, !errors.Is(err, api.ErrInvalidDir), "空 dir 应报 ErrInvalidDir: %v", err)
	pairs := [][2]error{{api.ErrInvalidDir, api.ErrPersist}, {api.ErrPersist, api.ErrCorrupt}, {api.ErrCorrupt, api.ErrInvalidDir}}
	for _, p := range pairs {
		fatalf(t, errors.Is(p[0], p[1]) || errors.Is(p[1], p[0]), "错误 %v 与 %v 不可区分", p[0], p[1])
	}
	fatalf(t, mustNew(t, t.TempDir()).SelfCheck() != nil, "SelfCheck 未通过")
}

// TestEightStepSequence 钉住 NOTES.md 八步推导表（不变量 2）。
func TestEightStepSequence(t *testing.T) {
	ops := []byte{'n', 'n', 'n', 'n', 'c', 'r', 'n', 'n'}
	wantRet := []int64{0, 1, 2, 3, -1, -1, 4, 5}
	wantPos := []int64{1, 2, 3, 4, 4, 4, 5, 6}
	dir := t.TempDir()
	a := mustNew(t, dir)
	for i, op := range ops {
		var err error
		switch op {
		case 'n':
			var n int64
			n, err = a.Next()
			fatalf(t, n != wantRet[i], "步 %d Next = %d 应 %d", i+1, n, wantRet[i])
		case 'c':
			a.SimulateCrash()
		case 'r':
			err = a.Recover()
		}
		fatalf(t, err != nil, "步 %d: %v", i+1, err)
		fatalf(t, posOf(dir) != wantPos[i], "步 %d 后位点应 %d", i+1, wantPos[i])
	}
}

// TestCrashRecoverNoDupNoHole 钉住不变量 1、3：任意崩溃点恢复后不重复、不空洞。
func TestCrashRecoverNoDupNoHole(t *testing.T) {
	for _, k := range []int{0, 1, 5, 50} {
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			a := mustNew(t, t.TempDir())
			seen := map[int64]bool{}
			for i := 0; i < k+10; i++ {
				if i == k {
					a.SimulateCrash()
					fatalf(t, a.Recover() != nil, "Recover 失败")
				}
				n, err := a.Next()
				fatalf(t, err != nil || seen[n], "第 %d 次 Next = %d,%v 出错或重复", i, n, err)
				seen[n] = true
			}
			for v := int64(0); v < int64(k+10); v++ {
				fatalf(t, !seen[v], "编号 %d 空洞", v)
			}
		})
	}
}

// TestFailureLeavesNoTrace 钉住不变量 4：故障注入均被拒、可判定、不留痕。
func TestFailureLeavesNoTrace(t *testing.T) {
	cases := []struct {
		name    string
		inject  func(string)
		op      func(*api.Allocator) error
		wantErr error
		posSame bool
	}{
		{"持久化失败", func(d string) { os.Chmod(d, 0o500) },
			func(a *api.Allocator) error { _, err := a.Next(); return err }, api.ErrPersist, true},
		{"checkpoint 损坏", func(d string) { os.WriteFile(filepath.Join(d, "checkpoint"), []byte("bad"), 0o644) },
			func(a *api.Allocator) error { return a.Recover() }, api.ErrCorrupt, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			a := mustNew(t, dir)
			a.Next()
			a.Next()
			before := posOf(dir)
			c.inject(dir)
			err := c.op(a)
			os.Chmod(dir, 0o700)
			fatalf(t, !errors.Is(err, c.wantErr), "应报 %v: %v", c.wantErr, err)
			fatalf(t, c.posSame && posOf(dir) != before, "位点被改变")
			n, err := a.Next()
			fatalf(t, err != nil || n != 2, "状态被改变: Next = %d,%v 应 2", n, err)
		})
	}
}

// TestConcurrent 并发 Next：编号集合恰为 {0,..,M-1}，无重复无空洞。
func TestConcurrent(t *testing.T) {
	for _, c := range []struct{ g, per int }{{4, 100}, {8, 128}, {16, 64}} {
		t.Run(fmt.Sprintf("%dx%d", c.g, c.per), func(t *testing.T) {
			a := mustNew(t, t.TempDir())
			total := c.g * c.per
			ch := make(chan int64, total)
			var wg sync.WaitGroup
			for i := 0; i < c.g; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for j := 0; j < c.per; j++ {
						if n, err := a.Next(); err == nil {
							ch <- n
						}
					}
				}()
			}
			wg.Wait()
			close(ch)
			seen, cnt := make([]bool, total), 0
			for n := range ch {
				fatalf(t, n < 0 || n >= int64(total) || seen[n], "编号 %d 越界或重复", n)
				seen[n] = true
				cnt++
			}
			fatalf(t, cnt != total, "发放 %d 个，应 %d", cnt, total)
		})
	}
}
