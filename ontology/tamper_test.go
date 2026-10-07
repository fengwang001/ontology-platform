package ontology

import "testing"

// TestImmutableAndHashChain 记录只读；内容篡改与删除都会被哈希链检测到。
func TestImmutableAndHashChain(t *testing.T) {
	store, exec := newSeeded(t, "A")
	r1, err := exec.ExecuteAction("a1", map[string]string{"A": "v1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.ExecuteAction("a2", map[string]string{"A": "v2"}, false); err != nil {
		t.Fatal(err)
	}

	// 公共 API 返回深拷贝：外部改动无法影响内部不可变序列。
	cp, _ := store.Get(r1.Seq)
	cp.Changes[0].After = "FORGED"
	cp.Hash = "deadbeef"
	if got, _ := store.Get(r1.Seq); got.Changes[0].After != "v1" {
		t.Fatal("公共视图必须是隔离的深拷贝")
	}
	if err := store.verifyHashChain(); err != nil {
		t.Fatalf("正常序列哈希链应通过: %v", err)
	}

	// 检测点1：直接篡改某条记录内容 -> 其自哈希不再匹配。
	store.records[0].Changes[0].After = "TAMPERED"
	if err := store.verifyHashChain(); err == nil {
		t.Fatal("篡改记录内容必须被哈希链拒绝")
	} else {
		t.Logf("依据1: 内容篡改被检测 -> %v", err)
	}

	// 检测点2：物理删除一条记录 -> 序号断裂 / 链接断裂。
	fresh, exec2 := newSeeded(t, "A")
	if _, err := exec2.ExecuteAction("x1", map[string]string{"A": "1"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := exec2.ExecuteAction("x2", map[string]string{"A": "2"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := exec2.ExecuteAction("x3", map[string]string{"A": "3"}, false); err != nil {
		t.Fatal(err)
	}
	// 物理删除中间一条：序号在位置上断裂、PrevHash 也断裂，必须被检测。
	fresh.records = append(fresh.records[:1], fresh.records[2:]...)
	if err := fresh.verifyHashChain(); err == nil {
		t.Fatal("删除中间记录必须被哈希链检测到")
	} else {
		t.Logf("依据2: 删除中间记录被检测 -> %v", err)
	}
	if _, err := NewExecutor(fresh); err == nil {
		t.Fatal("执行器必须拒绝基于被破坏的审计序列恢复")
	}
	_ = store
}
