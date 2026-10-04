package alias_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/alias"
	"ontology/indexreg"
	"ontology/resolve"
)

type opResult struct {
	err    error
	read   []resolve.MemberView
	write  string
	writeE error
	epoch  uint64
}

func mustSetup(t *testing.T, names ...string) *alias.Manager {
	t.Helper()
	m := alias.NewManager(indexreg.New())
	for _, n := range names {
		if err := m.CreateIndex(n); err != nil {
			t.Fatalf("setup CreateIndex(%q): %v", n, err)
		}
	}
	return m
}

func readNames(v []resolve.MemberView) []string {
	out := make([]string, 0, len(v))
	for _, mem := range v {
		out = append(out, mem.Index)
	}
	return out
}

func resolveRW(m *alias.Manager, name string) ([]resolve.MemberView, string, error) {
	r := resolve.New()
	v := m.SnapshotView()
	rs, err := r.Read(v, name)
	if err != nil {
		return nil, "", err
	}
	ws, werr := r.Write(v, name, resolve.NewTracer())
	return rs, ws, werr
}

func TestWriteInferenceThreeCasesAndExplicitFalse(t *testing.T) {
	cases := []struct {
		name   string
		acts   []alias.Action
		wantW  string
		wantOK bool
		wantR  []string
	}{
		{
			name:   "single unspecified -> sole member",
			acts:   []alias.Action{alias.Add("a", "i1", alias.WriteUnspecified, "")},
			wantW:  "i1",
			wantOK: true,
			wantR:  []string{"i1"},
		},
		{
			name: "single explicit true",
			acts: []alias.Action{
				alias.Add("a", "i1", alias.WriteFalse, ""),
				alias.Add("a", "i1", alias.WriteTrue, ""),
			},
			wantW:  "i1",
			wantOK: true,
			wantR:  []string{"i1"},
		},
		{
			name: "multiple unspecified -> none",
			acts: []alias.Action{
				alias.Add("a", "i1", alias.WriteUnspecified, ""),
				alias.Add("a", "i2", alias.WriteUnspecified, ""),
			},
			wantW:  "",
			wantOK: false,
			wantR:  []string{"i1", "i2"},
		},
		{
			name: "one explicit true among many",
			acts: []alias.Action{
				alias.Add("a", "i1", alias.WriteUnspecified, ""),
				alias.Add("a", "i2", alias.WriteUnspecified, ""),
				alias.Add("a", "i2", alias.WriteTrue, ""),
			},
			wantW:  "i2",
			wantOK: true,
			wantR:  []string{"i1", "i2"},
		},
		{
			name: "single explicit false -> none",
			acts: []alias.Action{
				alias.Add("a", "i1", alias.WriteUnspecified, ""),
				alias.Add("a", "i1", alias.WriteFalse, ""),
			},
			wantW:  "",
			wantOK: false,
			wantR:  []string{"i1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := mustSetup(t, "i1", "i2")
			for _, a := range tc.acts {
				if err := m.Update([]alias.Action{a}); err != nil {
					t.Fatalf("Update: %v", err)
				}
			}
			gotW, gotOK := m.WriteIndex("a")
			if gotOK != tc.wantOK || gotW != tc.wantW {
				t.Fatalf("WriteIndex(a) = (%q,%v), want (%q,%v)", gotW, gotOK, tc.wantW, tc.wantOK)
			}
			rs, _, werr := resolveRW(m, "a")
			if tc.wantOK {
				if werr != nil {
					t.Fatalf("ResolveWrite: %v", werr)
				}
			} else if !errors.Is(werr, alias.ErrNoWriteIndex) {
				t.Fatalf("ResolveWrite err = %v, want ErrNoWriteIndex", werr)
			}
			gotR := readNames(rs)
			if fmt.Sprint(gotR) != fmt.Sprint(tc.wantR) {
				t.Fatalf("ResolveRead = %v, want %v", gotR, tc.wantR)
			}
			t.Logf("input=%v 推断写索引=%q(ok=%v) Read=%v 依据=%s", tc.acts, gotW, gotOK, gotR, tc.name)
		})
	}
}

func TestSpecWorkflowWriteSwitch(t *testing.T) {
	m := mustSetup(t, "i1", "i2")
	r := resolve.New()
	upd := func(acts ...alias.Action) {
		t.Helper()
		if err := m.Update(acts); err != nil {
			t.Fatalf("Update %v: %v", acts, err)
		}
	}

	upd(alias.Add("a", "i1", alias.WriteUnspecified, ""))
	if w, ok := m.WriteIndex("a"); !ok || w != "i1" {
		t.Fatalf("want write i1, got %q %v", w, ok)
	}
	upd(alias.Add("a", "i2", alias.WriteUnspecified, ""))
	if _, ok := m.WriteIndex("a"); ok {
		t.Fatal("two unspecified members must yield no write index")
	}
	upd(alias.Add("a", "i2", alias.WriteTrue, ""))
	if w, _ := m.WriteIndex("a"); w != "i2" {
		t.Fatalf("want write i2, got %q", w)
	}

	// 单独把 i1 置真：终态两个真 -> 多写索引。
	err := m.Update([]alias.Action{alias.Add("a", "i1", alias.WriteTrue, "")})
	if !errors.Is(err, alias.ErrMultipleWrite) {
		t.Fatalf("want ErrMultipleWrite, got %v", err)
	}

	// 同批交换写索引：中间态两个真，终态仅 i1 真 -> 接受。
	upd(alias.Add("a", "i1", alias.WriteTrue, ""), alias.Add("a", "i2", alias.WriteFalse, ""))
	if w, _ := m.WriteIndex("a"); w != "i1" {
		t.Fatalf("want switched write i1, got %q", w)
	}

	// 移除唯一真成员，剩 i2 显式假 -> 无写索引。
	upd(alias.Remove("a", "i1", true))
	if _, ok := m.WriteIndex("a"); ok {
		t.Fatal("sole explicit-false member must yield no write index")
	}
	rs, rerr := r.Read(m.SnapshotView(), "a")
	if rerr != nil {
		t.Fatalf("read: %v", rerr)
	}
	if got := readNames(rs); fmt.Sprint(got) != "[i2]" {
		t.Fatalf("read = %v, want [i2]", got)
	}
	_, werr := r.Write(m.SnapshotView(), "a", resolve.NewTracer())
	if !errors.Is(werr, alias.ErrNoWriteIndex) {
		t.Fatalf("want ErrNoWriteIndex, got %v", werr)
	}
}

func TestRenameIndexToAliasBothOrders(t *testing.T) {
	build := func(t *testing.T) *alias.Manager { return mustSetup(t, "i1", "i2") }

	// 次序一：先删索引再建别名。
	m := build(t)
	if err := m.Update([]alias.Action{
		alias.RemoveIndex("i1"),
		alias.Add("i1", "i2", alias.WriteUnspecified, ""),
	}); err != nil {
		t.Fatalf("rename order1: %v", err)
	}
	if w, ok := m.WriteIndex("i1"); !ok || w != "i2" {
		t.Fatalf("order1 write = %q,%v", w, ok)
	}

	// 次序二：先建别名（中间态同名）再删索引。
	m = build(t)
	if err := m.Update([]alias.Action{
		alias.Add("i1", "i2", alias.WriteUnspecified, ""),
		alias.RemoveIndex("i1"),
	}); err != nil {
		t.Fatalf("rename order2: %v", err)
	}
	if w, ok := m.WriteIndex("i1"); !ok || w != "i2" {
		t.Fatalf("order2 write = %q,%v", w, ok)
	}

	// i1 仍是索引时单独 Add 同名别名 -> 终态名字冲突（独立 manager，拒绝后状态不变）。
	mRejected := build(t)
	err := mRejected.Update([]alias.Action{alias.Add("i1", "i2", alias.WriteUnspecified, "")})
	if !errors.Is(err, indexreg.ErrNameConflict) {
		t.Fatalf("want ErrNameConflict, got %v", err)
	}

	// 关闭写索引后：读空列表、写报已关闭、直接读索引报已关闭。
	// 在“已接受改名（次序二）”的 manager 上验证读/写不对称。
	if err := m.CloseIndex("i2"); err != nil {
		t.Fatalf("CloseIndex: %v", err)
	}
	r := resolve.New()
	v := m.SnapshotView()
	rs, err := r.Read(v, "i1")
	if err != nil || len(rs) != 0 {
		t.Fatalf("Read alias after close = %v,%v", rs, err)
	}
	if _, err = r.Write(v, "i1", resolve.NewTracer()); !errors.Is(err, indexreg.ErrIndexClosed) {
		t.Fatalf("Write alias after close: %v", err)
	}
	if _, err = r.Read(v, "i2"); !errors.Is(err, indexreg.ErrIndexClosed) {
		t.Fatalf("Read closed index: %v", err)
	}
}

func TestMustExistTwoStates(t *testing.T) {
	m := mustSetup(t, "i1")
	if err := m.Update([]alias.Action{alias.Remove("a", "i1", false)}); err != nil {
		t.Fatalf("mustExist=false on missing member should be no-op accept: %v", err)
	}
	err := m.Update([]alias.Action{alias.Remove("a", "i1", true)})
	if !errors.Is(err, alias.ErrMemberNotFound) {
		t.Fatalf("want ErrMemberNotFound, got %v", err)
	}
	var ae *alias.ActionError
	if !errors.As(err, &ae) || ae.Index != 0 {
		t.Fatalf("want ActionError index 0, got %#v", err)
	}
}

func TestRejectionOrderingAndEpochRules(t *testing.T) {
	m := mustSetup(t, "i1", "i2", "z1")

	// 参数非法：空批、超 100、非法名。
	if err := m.Update(nil); !errors.Is(err, alias.ErrInvalidArgument) {
		t.Fatalf("empty batch: %v", err)
	}
	big := make([]alias.Action, 101)
	for i := range big {
		big[i] = alias.Add("a", "i1", alias.WriteUnspecified, "")
	}
	if err := m.Update(big); !errors.Is(err, alias.ErrInvalidArgument) {
		t.Fatalf("oversized batch: %v", err)
	}
	if err := m.Update([]alias.Action{alias.Add("Bad", "i1", alias.WriteUnspecified, "")}); !errors.Is(err, alias.ErrInvalidArgument) {
		t.Fatalf("bad alias name: %v", err)
	}
	if m.Epoch() != 3 {
		t.Fatalf("rejected batches must not bump epoch, got %d", m.Epoch())
	}

	// 最小下标逐步错误优先于终态错误。
	// 动作0 Add 到不存在索引（逐步错误）；即便终态也有多写/冲突，也先报下标0。
	err := m.Update([]alias.Action{
		alias.Add("a", "ghost", alias.WriteUnspecified, ""),
		alias.Add("z1", "i2", alias.WriteUnspecified, ""), // 终态名字冲突
	})
	var ae *alias.ActionError
	if !errors.As(err, &ae) || ae.Index != 0 || !errors.Is(err, indexreg.ErrIndexNotFound) {
		t.Fatalf("want step error at 0 index-not-found, got %v", err)
	}

	// 名字冲突优先于多写索引：构造终态同时违反二者。
	if err := m.Update([]alias.Action{
		alias.Add("a", "i1", alias.WriteTrue, ""),
		alias.Add("a", "i2", alias.WriteTrue, ""),         // 终态多写
		alias.Add("z1", "i1", alias.WriteUnspecified, ""), // 终态名字冲突(z1 是索引)
	}); !errors.Is(err, indexreg.ErrNameConflict) {
		t.Fatalf("name conflict before multi-write: %v", err)
	}

	// 被拒不改状态与纪元。
	before := m.Epoch()
	_ = m.Update([]alias.Action{alias.RemoveIndex("ghost")})
	if m.Epoch() != before {
		t.Fatalf("rejected RemoveIndex changed epoch %d -> %d", before, m.Epoch())
	}

	// 空效果批：重复 Remove(mustExist=false) 接受但不加纪元。
	if err := m.Update([]alias.Action{alias.Remove("nope", "i1", false)}); err != nil {
		t.Fatalf("no-op remove: %v", err)
	}
	if m.Epoch() != before {
		t.Fatalf("no-effect batch bumped epoch %d -> %d", before, m.Epoch())
	}

	// 有效提交加纪元；重复提交相同终态不再加。
	if err := m.Update([]alias.Action{alias.Add("a", "i1", alias.WriteUnspecified, "")}); err != nil {
		t.Fatalf("add: %v", err)
	}
	after := m.Epoch()
	if after != before+1 {
		t.Fatalf("effective batch epoch %d, want %d", after, before+1)
	}
	// 再提交一次完全相同的 Add（覆盖为相同值）：接受但不加纪元。
	if err := m.Update([]alias.Action{alias.Add("a", "i1", alias.WriteUnspecified, "")}); err != nil {
		t.Fatalf("same-state add: %v", err)
	}
	if m.Epoch() != after {
		t.Fatalf("same terminal state bumped epoch %d -> %d", after, m.Epoch())
	}
}

func TestTouchedBound(t *testing.T) {
	for _, n := range []int{2, 2000} {
		t.Run(fmt.Sprintf("members=%d", n), func(t *testing.T) {
			names := make([]string, 0, n)
			acts := make([]alias.Action, 0, n)
			for i := 0; i < n; i++ {
				name := fmt.Sprintf("i%05d", i)
				names = append(names, name)
				flag := alias.WriteUnspecified
				if i == n-1 {
					flag = alias.WriteTrue
				}
				acts = append(acts, alias.Add("a", name, flag, ""))
			}
			m := mustSetup(t, names...)
			for start := 0; start < len(acts); start += 100 {
				end := start + 100
				if end > len(acts) {
					end = len(acts)
				}
				if err := m.Update(acts[start:end]); err != nil {
					t.Fatalf("seed batch [%d:%d]: %v", start, end, err)
				}
			}
			tracer := resolve.NewTracer()
			r := resolve.New()
			w, err := r.Write(m.SnapshotView(), "a", tracer)
			if err != nil || w != names[n-1] {
				t.Fatalf("Write = %q,%v", w, err)
			}
			if got := tracer.Touched(); got > 1 {
				t.Fatalf("ResolveWrite touched %d member records with %d members, want <= 1", got, n)
			}
			t.Logf("成员数=%d ResolveWrite 触碰记录数=%d 写索引=%q 依据=推定表直取唯一真成员", n, tracer.Touched(), w)
		})
	}
}

func TestConcurrentSerializability(t *testing.T) {
	m := mustSetup(t)
	const workers = 8
	const iterations = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				name := fmt.Sprintf("idx%02d", (w*iterations+i)%16)
				switch i % 4 {
				case 0:
					_ = m.CreateIndex(name)
				case 1:
					_ = m.CloseIndex(name)
				case 2:
					_ = m.OpenIndex(name)
				case 3:
					_ = m.Update([]alias.Action{
						alias.Add("a", name, alias.WriteUnspecified, ""),
						alias.Remove("a", name, false),
					})
				}
				_ = m.Epoch()
				_, _ = resolve.New().Read(m.SnapshotView(), "a")
			}
		}(w)
	}
	wg.Wait()
}
