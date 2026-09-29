package di

import (
	"testing"
)

func TestTransientOwnershipAndReleaseOnce(t *testing.T) {
	log := &orderLog{}
	sCtl := &ctl{log: log}
	rootTrCtl := &ctl{log: log}
	scTrCtl := &ctl{log: log}
	scCtl := &ctl{log: log}
	c := New()
	mustReg(t, c, Registration{Name: "s", Lifetime: Singleton, Construct: sCtl.ctor("s")})
	mustReg(t, c, Registration{Name: "rt", Lifetime: Transient, Dependencies: []string{"s"}, Construct: rootTrCtl.ctor("rt")})
	mustReg(t, c, Registration{Name: "sc", Lifetime: Scoped, Construct: scCtl.ctor("sc")})
	mustReg(t, c, Registration{Name: "st", Lifetime: Transient, Dependencies: []string{"sc", "s"}, Construct: scTrCtl.ctor("st")})
	mustFreeze(t, c)

	sc, _ := c.NewScope("s")
	if _, err := sc.Resolve("st"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Resolve("rt"); err != nil {
		t.Fatal(err)
	}

	// 关闭作用域：st 随作用域释放，sc 先于 st（依赖者后构造、先释放），s 仍存活。
	if err := sc.Close(); err != nil {
		t.Fatal(err)
	}
	_, dis := log.snapshot()
	t.Logf("input scope close -> dispose=%v | 判定依据: 作用域内瞬态随作用域, 单例不释放", dis)
	if len(dis) != 2 || dis[0] != "st" || dis[1] != "sc" {
		t.Fatalf("want [st sc], got %v", dis)
	}

	// 容器关闭：归根瞬态 rt 与单例 s 按逆构造顺序释放（s 先于 rt），每个恰好一次。
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	_, dis = log.snapshot()
	t.Logf("input container close -> full dispose=%v | 判定依据: 归根瞬态随根, 每个实例恰好释放一次", dis)
	counts := map[string]int{}
	for _, n := range dis {
		counts[n]++
	}
	for name, want := range map[string]int{"st": 1, "sc": 1, "rt": 1, "s": 1} {
		if counts[name] != want {
			t.Fatalf("%s released %d times, want %d (full=%v)", name, counts[name], want, dis)
		}
	}
	if indexOf(dis, "rt") > indexOf(dis, "s") {
		t.Fatalf("singleton s must dispose before root transient rt: %v", dis)
	}
}

func TestDiamondScopedSharedDisposedOnce(t *testing.T) {
	log := &orderLog{}
	aCtl := &ctl{log: log}
	b1Ctl := &ctl{log: log}
	b2Ctl := &ctl{log: log}
	scCtl := &ctl{log: log}
	c := New()
	// a 依赖 b1、b2，两者都依赖同一作用域实例 sc（菱形）
	mustReg(t, c, Registration{Name: "a", Lifetime: Transient, Dependencies: []string{"b1", "b2"}, Construct: aCtl.ctor("a")})
	mustReg(t, c, Registration{Name: "b1", Lifetime: Transient, Dependencies: []string{"sc"}, Construct: b1Ctl.ctor("b1")})
	mustReg(t, c, Registration{Name: "b2", Lifetime: Transient, Dependencies: []string{"sc"}, Construct: b2Ctl.ctor("b2")})
	mustReg(t, c, Registration{Name: "sc", Lifetime: Scoped, Construct: scCtl.ctor("sc")})
	mustFreeze(t, c)
	sc, _ := c.NewScope("s")
	v, err := sc.Resolve("a")
	if err != nil {
		t.Fatal(err)
	}
	root := v.(*rec)
	if root.deps["b1"].deps["sc"] != root.deps["b2"].deps["sc"] {
		t.Fatal("diamond branches did not share the scoped instance")
	}
	if err := sc.Close(); err != nil {
		t.Fatal(err)
	}
	_, dis := log.snapshot()
	t.Logf("input diamond close -> dispose=%v | 判定依据: 共享作用域实例只释放一次且最后释放", dis)
	count := 0
	for _, n := range dis {
		if n == "sc" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("shared scoped instance disposed %d times, want 1: %v", count, dis)
	}
	if dis[len(dis)-1] != "sc" {
		t.Fatalf("shared dependency sc must be disposed last, got %v", dis)
	}
}
