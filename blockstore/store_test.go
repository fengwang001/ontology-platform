package blockstore

import (
	"errors"
	"testing"
)

func TestCommitRejectionReasonsAndNoSideEffects(t *testing.T) {
	st := New(0)
	st.BeginSession("s1")
	must(t, st.Upload("s1", dstr(1), []byte("hello")))

	// 清单引用未上传且库中不存在的块。
	err := st.Commit("s1", "m-missing", []Digest{dstr(99)})
	if !errors.Is(err, ErrBlockMissing) {
		t.Fatalf("want ErrBlockMissing, got %v", err)
	}
	if _, gerr := st.ReadManifest("m-missing"); !errors.Is(gerr, ErrManifestNotFound) {
		t.Fatalf("rejected commit left manifest: %v", gerr)
	}

	// 会话不存在。
	if err := st.Commit("ghost", "m1", []Digest{dstr(1)}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}

	// 正常提交一次。
	must(t, st.Commit("s1", "m1", []Digest{dstr(1)}))

	// 同一会话重复提交。
	if err := st.Commit("s1", "m2", []Digest{dstr(1)}); !errors.Is(err, ErrAlreadyCommitted) {
		t.Fatalf("want ErrAlreadyCommitted, got %v", err)
	}

	// 已结束会话提交。
	st.BeginSession("s2")
	must(t, st.EndSession("s2"))
	if err := st.Commit("s2", "m3", []Digest{dstr(1)}); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("want ErrSessionClosed, got %v", err)
	}
	if _, gerr := st.ReadManifest("m3"); !errors.Is(gerr, ErrManifestNotFound) {
		t.Fatalf("commit after end must not leave manifest: %v", gerr)
	}

	// 缺块拒绝不得改变任何块状态。
	st.BeginSession("s3")
	if err := st.Commit("s3", "m4", []Digest{dstr(99)}); !errors.Is(err, ErrBlockMissing) {
		t.Fatalf("want ErrBlockMissing, got %v", err)
	}
	if st.StateOf(dstr(1)) != StateNormal {
		t.Fatalf("block state changed after rejected commit: %s", st.StateOf(dstr(1)))
	}
}

func TestCapacityFull(t *testing.T) {
	st := New(5)
	st.BeginSession("s1")
	if err := st.Upload("s1", dstr(1), []byte("abc")); err != nil {
		t.Fatalf("upload within capacity failed: %v", err)
	}
	err := st.Upload("s1", dstr(2), []byte("xyz123"))
	if !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("want ErrCapacityFull, got %v", err)
	}
	if got := st.StateOf(dstr(2)); got != StateDeleted {
		t.Fatalf("rejected upload left block, state=%s", got)
	}
	// 去重复用不占新容量。
	if err := st.Upload("s1", dstr(1), []byte("abc")); err != nil {
		t.Fatalf("dedup upload failed: %v", err)
	}
	if st.used != 3 {
		t.Fatalf("used=%d, want 3", st.used)
	}
}
