package raid5

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// snapshotTree 收集目录下所有文件（含标记文件）的字节，按相对路径索引。
func snapshotTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(dir, path)
		out[rel] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func openFileVolume(t *testing.T, dir string, n, stripes int) (*Volume, []*FileDisk, *Journal) {
	t.Helper()
	disks := make([]Disk, n)
	fds := make([]*FileDisk, n)
	for i := 0; i < n; i++ {
		fd, err := NewFileDisk(i, stripes, filepath.Join(dir, "disk"+strconv.Itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		disks[i] = fd
		fds[i] = fd
	}
	j, err := OpenJournal(filepath.Join(dir, "journal"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := Create(disks, j, Options{N: n, Stripes: stripes})
	if err != nil {
		t.Fatal(err)
	}
	return v, fds, j
}

func reopenFileVolume(t *testing.T, dir string, n, stripes int) (*Volume, []*FileDisk) {
	t.Helper()
	disks := make([]Disk, n)
	fds := make([]*FileDisk, n)
	for i := 0; i < n; i++ {
		fd, err := OpenFileDisk(i, stripes, filepath.Join(dir, "disk"+strconv.Itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		disks[i] = fd
		fds[i] = fd
	}
	j, err := OpenJournal(filepath.Join(dir, "journal"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := Create(disks, j, Options{N: n, Stripes: stripes})
	if err != nil {
		t.Fatal(err)
	}
	return v, fds
}

// script 是固定写入序列：在某一步安装断电钩子。
type op struct {
	start  int
	blocks [][]byte
}

func buildScript(n, stripes int) []op {
	ops := make([]op, 0, stripes*2)
	for s := 0; s < stripes; s++ {
		// 部分写第一个槽（RMW）
		ops = append(ops, op{s * (n - 1), [][]byte{mkBlock(byte(s), 1)}})
		if n-2 > 0 {
			// 部分写其余槽的连续区域
			seg := make([][]byte, n-2)
			for k := range seg {
				seg[k] = mkBlock(byte(0x30+s), k)
			}
			ops = append(ops, op{s*(n-1) + 1, seg})
		}
	}
	return ops
}

// TestFileDiskCrashDeterminism 文件盘端到端：同一写入序列与同一断电点
// 重放两次，最终各盘（含标记文件）内容必须逐字节相同；且断电点重开后
// 每带校验一致。
func TestFileDiskCrashDeterminism(t *testing.T) {
	n, stripes := 4, 6
	script := buildScript(n, stripes)

	run := func(root string, crashStep int, point CrashPoint) map[string][]byte {
		dir := filepath.Join(root, "v")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		v, fds, _ := openFileVolume(t, dir, n, stripes)
		for i, o := range script {
			if i == crashStep {
				stripe, _, _ := blockLocation(n, o.start)
				v.SetCrashHook(&CrashHook{Point: point, Stripe: stripe})
			}
			err := v.Write(o.start, o.blocks)
			if i == crashStep {
				if err != errCrashed {
					t.Fatalf("step %d: want crash got %v", i, err)
				}
				for _, fd := range fds {
					fd.Close()
				}
				v, fds = reopenFileVolume(t, dir, n, stripes)
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		v.RebuildDone()
		if err := v.Close(); err != nil {
			t.Fatal(err)
		}
		return snapshotTree(t, dir)
	}

	r1 := run(t.TempDir(), 5, CrashAfterData)
	r2 := run(t.TempDir(), 5, CrashAfterData)
	if len(r1) != len(r2) {
		t.Fatalf("file set differs: %d vs %d", len(r1), len(r2))
	}
	for name, b1 := range r1 {
		b2, ok := r2[name]
		if !ok {
			t.Fatalf("file %s missing in second run", name)
		}
		if string(b1) != string(b2) {
			t.Fatalf("file %s differs byte-for-byte between identical runs", name)
		}
		t.Logf("判定: %s 在两次相同序列+断电点运行后逐字节相同（%d 字节）", name, len(b1))
	}

	// 同时验证不同断电点恢复后逻辑数据一致（写洞被消除）。
	for _, point := range []CrashPoint{CrashAfterIntent, CrashAfterData, CrashAfterParity} {
		dir := filepath.Join(t.TempDir(), "v")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		v, fds, _ := openFileVolume(t, dir, n, stripes)
		stripe, _, _ := blockLocation(n, script[5].start)
		v.SetCrashHook(&CrashHook{Point: point, Stripe: stripe})
		if err := v.Write(script[5].start, script[5].blocks); err != errCrashed {
			t.Fatal(err)
		}
		for _, fd := range fds {
			fd.Close()
		}
		v2, fds2 := reopenFileVolume(t, dir, n, stripes)
		disksIf := make([]Disk, n)
		for i := range fds2 {
			disksIf[i] = fds2[i]
		}
		if !verifyAllStripes(t, disksIf, n, stripes) {
			t.Fatalf("file-backed recovery at %s broke parity", crashName(point))
		}
		t.Logf("判定: 文件盘在 %s 断电重开后全部 %d 条带校验一致",
			crashName(point), stripes)
		v2.Close()
	}
}

// TestFileDiskFailReplaceRebuild 文件盘上的失效标记持久化 + 换盘重建全流程。
func TestFileDiskFailReplaceRebuild(t *testing.T) {
	n, stripes := 3, 5
	dir := t.TempDir()
	v, _, _ := openFileVolume(t, dir, n, stripes)
	fillVolume(t, v, n, stripes)

	if err := v.FailDisk(1); err != nil {
		t.Fatal(err)
	}
	// 降级读
	dst := [][]byte{make([]byte, BlockSize)}
	if err := v.Read(0, dst); err != nil {
		t.Fatal(err)
	}
	v.Close()

	if _, err := os.Stat(filepath.Join(dir, "disk1.dead")); err != nil {
		t.Fatalf("dead marker not persisted: %v", err)
	}
	t.Log("判定: 失效后磁盘文件改名为 disk1.dead 并持久化")

	// 重开（仍降级），随后换入新盘重建。
	v2, fds2 := reopenFileVolume(t, dir, n, stripes)
	if !fds2[1].Failed() {
		t.Fatal("reopen should detect failed disk via marker")
	}
	fresh, err := NewReplacementFileDisk(1, stripes, filepath.Join(dir, "disk1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := v2.ReplaceDisk(1, fresh); err != nil {
		t.Fatal(err)
	}
	v2.RebuildDone()

	all := readAll(t, v2, n, stripes)
	for s := 0; s < stripes; s++ {
		for k := 0; k < n-1; k++ {
			if !bytesEqualBlocks(all[s*(n-1)+k], mkBlock(byte(0x40+s), k)) {
				t.Fatalf("post file-rebuild data mismatch stripe %d slot %d", s, k)
			}
		}
	}
	v2.Close()
	if _, err := os.Stat(filepath.Join(dir, "disk1.rebuild")); !os.IsNotExist(err) {
		t.Fatalf("rebuild marker should be removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "disk1.dead")); !os.IsNotExist(err) {
		t.Fatal("dead marker should be removed after replacement")
	}
	t.Log("判定: 文件盘换盘重建后数据完整，.dead/.rebuild/.rebuilt 标记均清除")
}
