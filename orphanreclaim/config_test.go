package orphanreclaim

import (
	"errors"
	"testing"
)

func TestConfigErrors_FixedOrder(t *testing.T) {
	// (3) 联合组引用未定义类型：优先于 (4) 宽限期非正数。
	bad := Config{
		Rules: map[string]LinkRule{
			"A": {Type: "A", Kind: JointRetention, JointGroup: []string{"MISSING"}},
		},
		GraceGen1: 0,
		GraceGen2: -1,
	}
	if _, err := New(bad, SystemClock(), nil, nil); !errors.Is(err, ErrJointRefUndefined) {
		t.Fatalf("want ErrJointRefUndefined reported first, got %v", err)
	}

	// 仅 (4)：合法联合组 + 非正宽限期。
	bad = Config{
		Rules: map[string]LinkRule{
			"A": {Type: "A", Kind: JointRetention, JointGroup: []string{"B"}},
			"B": {Type: "B", Kind: JointRetention, JointGroup: []string{"A"}},
		},
		GraceGen1: 0,
		GraceGen2: 5,
	}
	if _, err := New(bad, SystemClock(), nil, nil); !errors.Is(err, ErrInvalidGracePeriod) {
		t.Fatalf("want ErrInvalidGracePeriod, got %v", err)
	}

	// 单元素「联合」组没有伙伴，按第 (3) 类报错。
	bad = Config{
		Rules:     map[string]LinkRule{"A": {Type: "A", Kind: JointRetention}},
		GraceGen1: 1,
		GraceGen2: 1,
	}
	if _, err := New(bad, SystemClock(), nil, nil); !errors.Is(err, ErrJointRefUndefined) {
		t.Fatalf("singleton joint group: want ErrJointRefUndefined, got %v", err)
	}
}

func TestMutationErrors_FixedOrder(t *testing.T) {
	r, _, _ := newTestEngine(t, testConfig(), 0)
	r.CreateObject("o1")

	// (1) 目标不存在先于 (2) 类型未配置：即使类型也未配置，仍只报 (1)。
	if err := r.AddLink("o1", "ghost", "NOPE"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("want ErrObjectNotFound, got %v", err)
	}
	// 源不存在同样属于 (1)。
	if err := r.AddLink("ghost", "o1", "I"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("want ErrObjectNotFound(source), got %v", err)
	}
	// 目标存在但类型未配置 -> (2)。
	if err := r.AddLink("o1", "o1", "NOPE"); !errors.Is(err, ErrLinkTypeUnconfigured) {
		t.Fatalf("want ErrLinkTypeUnconfigured, got %v", err)
	}
	// RemoveLink 次序一致：不存在目标 + 未配置类型 -> (1)。
	if err := r.RemoveLink("o1", "ghost", "NOPE"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("remove: want ErrObjectNotFound, got %v", err)
	}
}

func TestOverlappingJointGroups_Merge(t *testing.T) {
	cfg := Config{
		Rules: map[string]LinkRule{
			"A": {Type: "A", Kind: JointRetention, JointGroup: []string{"B"}},
			"B": {Type: "B", Kind: JointRetention, JointGroup: []string{"C"}},
			"C": {Type: "C", Kind: JointRetention, JointGroup: []string{"A"}},
		},
		GraceGen1: 10,
		GraceGen2: 5,
	}
	r, c, _ := newTestEngine(t, cfg, 100)
	r.CreateObject("t")
	r.CreateObject("a")
	r.CreateObject("b")

	// A、B 同时存在仍不足：合并后的组是 {A,B,C}。
	mustAdd(t, r, "a", "t", "A")
	mustAdd(t, r, "b", "t", "B")
	assertGen(t, r, "t", Gen1)

	c.t = 200
	r.CreateObject("c3")
	mustAdd(t, r, "c3", "t", "C")
	assertGen(t, r, "t", GenNone) // 三者齐备，立即救回
}
