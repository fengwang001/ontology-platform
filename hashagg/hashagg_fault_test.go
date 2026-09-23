package hashagg

import (
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ontology/acc"
	"ontology/hashpart"
	"ontology/verify"
)

// TestDeterministicReadback 打乱回读完成顺序 20 次，输出必须逐字节一致。
func TestDeterministicReadback(t *testing.T) {
	rows := repeatKeys(200, 3000)
	var want []acc.Group
	for trial := 0; trial < 20; trial++ {
		a, err := New(8, 50, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		a.readDelay = func(int) { time.Sleep(time.Duration(rand.IntN(3)) * time.Millisecond) }
		for _, r := range rows {
			if err := a.Add(r); err != nil {
				t.Fatal(err)
			}
		}
		got, err := a.Finish()
		if err != nil {
			t.Fatal(err)
		}
		if trial == 0 {
			want = got
		} else if !verify.Identical(want, got) {
			t.Fatalf("第%d次回读顺序下输出不一致", trial)
		}
	}
}

// TestWriteFailureAborts 注入第 k 次写分区失败：聚合中止且临时文件残留为 0。
func TestWriteFailureAborts(t *testing.T) {
	dir := t.TempDir()
	a, err := New(2, 4, dir)
	if err != nil {
		t.Fatal(err)
	}
	a.SetFailOnWrite(2)
	var addErr error
	for _, r := range distinct(20) {
		if addErr = a.Add(r); addErr != nil {
			break
		}
	}
	if !errors.Is(addErr, hashpart.ErrWrite) {
		t.Fatalf("期望 ErrWrite, 得到 %v", addErr)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("写失败后临时文件残留 %d 个", len(entries))
	}
}

// TestTruncatedSegmentAborts 截断的分区文件必须让聚合中止，错误指出分区与偏移。
func TestTruncatedSegmentAborts(t *testing.T) {
	dir := t.TempDir()
	a, err := New(2, 4, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range distinct(20) {
		if err := a.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("预期有溢出段文件: %v", err)
	}
	path := filepath.Join(dir, entries[0].Name())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data[:len(data)-3], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Finish(); err == nil {
		t.Fatal("截断的分区文件未被检出")
	} else {
		var fe *hashpart.FileError
		if !errors.As(err, &fe) || fe.Part < 0 || fe.Offset < 0 {
			t.Fatalf("错误未指出分区与偏移: %v", err)
		}
		for _, s := range []error{hashpart.ErrHeader, hashpart.ErrLengthPrefix, hashpart.ErrBody, hashpart.ErrCRC} {
			if errors.Is(err, s) {
				return
			}
		}
		t.Fatalf("错误不属于四类截断: %v", err)
	}
}
