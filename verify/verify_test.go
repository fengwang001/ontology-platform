package verify_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ontology/export"
	"ontology/manifest"
	"ontology/snapshot"
	"ontology/store"
	"ontology/verify"
)

var interrupt = errors.New("boom")

func build(t *testing.T, n, size int) (string, []byte, manifest.Manifest, *snapshot.Handle) {
	t.Helper()
	st := store.New()
	for i := 0; i < n; i++ {
		st.Put(fmt.Sprintf("k%04d", i), []byte(fmt.Sprintf("v%04d", i)))
	}
	dir := t.TempDir()
	snap := snapshot.Take(st)
	e, err := export.Run(export.Options{Dir: dir, Snapshot: snap, BlockSize: size})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, export.DataName))
	if err != nil {
		t.Fatal(err)
	}
	return dir, data, e.Manifest(), snap
}

// TestTruncateEveryByte：从 1 截断到 len-1，逐字节遍历，全部归类为四类之一。
func TestTruncateEveryByte(t *testing.T) {
	_, data, man, snap := build(t, 60, 85)
	defer snap.Close()
	counts := map[error]int{}
	for length := 1; length < len(data); length++ {
		d := filepath.Join(t.TempDir(), "x")
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, export.DataName), data[:length], 0o600); err != nil {
			t.Fatal(err)
		}
		if err := manifest.Save(d, man); err != nil {
			t.Fatal(err)
		}
		_, err := verify.File(d)
		switch {
		case errors.Is(err, export.ErrBlockHeader):
			counts[export.ErrBlockHeader]++
		case errors.Is(err, export.ErrBlockBody):
			counts[export.ErrBlockBody]++
		case errors.Is(err, export.ErrCRC):
			counts[export.ErrCRC]++
		case errors.Is(err, export.ErrBlockOrder):
			counts[export.ErrBlockOrder]++
		default:
			t.Fatalf("length=%d unclassified err=%v", length, err)
		}
	}
	want := map[error]int{
		export.ErrBlockHeader: 0,
		export.ErrBlockBody:   0,
		export.ErrCRC:         0,
		export.ErrBlockOrder:  0,
	}
	for class := range want {
		if counts[class] == 0 {
			t.Fatalf("class %v never observed; counts=%v", class, counts)
		}
	}
	// 清单缺失必须单独归类为“清单不完整”。
	d := filepath.Join(t.TempDir(), "y")
	_ = os.MkdirAll(d, 0o700)
	_ = os.WriteFile(filepath.Join(d, export.DataName), data[:len(data)-1], 0o600)
	if _, err := verify.File(d); !errors.Is(err, export.ErrManifestIncomplete) {
		t.Fatalf("missing manifest err=%v", err)
	}
}

// TestBlockOrder：删掉一块（缺失）与交换两块（错位）都要指名块号。
func TestBlockOrder(t *testing.T) {
	dir, data, man, snap := build(t, 80, 85)
	defer snap.Close()
	if _, err := verify.File(dir); err != nil {
		t.Fatalf("clean file failed: %v", err)
	}
	cases := []struct {
		name    string
		mutate  func([]byte, map[int]manifest.BlockInfo) []byte
		wantErr error
	}{
		{"missing-block", func(b []byte, m map[int]manifest.BlockInfo) []byte {
			return b[:m[1].Offset] // 块 1 起全部缺失
		}, export.ErrBlockOrder},
		{"swap-blocks", func(b []byte, m map[int]manifest.BlockInfo) []byte {
			b0 := m[0]
			b1 := m[1]
			tmp := make([]byte, b0.Length)
			copy(tmp, b[b0.Offset:b0.Offset+int64(b0.Length)])
			copy(b[b0.Offset:b0.Offset+int64(b0.Length)], b[b1.Offset:b1.Offset+int64(b1.Length)])
			copy(b[b1.Offset:b1.Offset+int64(b0.Length)], tmp)
			return b
		}, export.ErrBlockOrder},
		{"corrupt-crc", func(b []byte, m map[int]manifest.BlockInfo) []byte {
			b2 := m[2]
			b[b2.Offset+24] ^= 0xFF
			return b
		}, export.ErrCRC},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := filepath.Join(t.TempDir(), "z")
			_ = os.MkdirAll(d, 0o700)
			buf := append([]byte(nil), data...)
			buf = tc.mutate(buf, man.Blocks)
			_ = os.WriteFile(filepath.Join(d, export.DataName), buf, 0o600)
			_ = manifest.Save(d, man)
			_, err := verify.File(d)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
		})
	}
}

// TestCloseRace：导出进行中关闭快照——要么导出成功完整，要么收到可判定错误，
// 校验器绝不会把半截文件当成完整。
func TestCloseRace(t *testing.T) {
	st := store.New()
	for i := 0; i < 500; i++ {
		st.Put(fmt.Sprintf("k%04d", i), []byte("vvvv"))
	}
	for round := 0; round < 20; round++ {
		dir := t.TempDir()
		snap := snapshot.Take(st)
		done := make(chan error, 1)
		go func() {
			_, err := export.Run(export.Options{
				Dir: dir, Snapshot: snap, BlockSize: 64,
				Stop: func(int) error { return nil },
			})
			done <- err
		}()
		snap.Close()
		runErr := <-done
		if runErr != nil && !errors.Is(runErr, snapshot.ErrSnapshotClosed) {
			t.Fatalf("round %d unexpected err %v", round, runErr)
		}
		st.Put("extra", []byte("x"))
		if runErr == nil {
			snap2 := snapshot.Take(st)
			snap2.Close()
			if _, err := verify.File(dir); err != nil {
				t.Fatalf("round %d complete file failed verify: %v", round, err)
			}
		}
	}
}
