// demo 逐项演练配置快照管理器的行为并打印 OK/FAIL 判定。
// 不读命令行参数、不联网、不使用 time.Sleep；全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology"
)

var checks, failures int

func ok(cond bool, msg string) {
	checks++
	if cond {
		fmt.Println("OK   " + msg)
	} else {
		failures++
		fmt.Println("FAIL " + msg)
	}
}

func main() {
	schema, err := ontology.NewSchema(
		ontology.Field{Name: "host", Kind: ontology.StringKind},
		ontology.Field{Name: "port", Kind: ontology.IntKind, Min: 1, Max: 65535},
		ontology.Field{Name: "retries", Kind: ontology.IntKind, Min: 0, Max: 10},
	)
	if err != nil {
		fmt.Println("FAIL build schema: " + err.Error())
		os.Exit(1)
	}
	m, err := ontology.NewManager(schema, map[string]any{
		"host": "alpha", "port": int64(80), "retries": int64(3),
	})
	if err != nil {
		fmt.Println("FAIL build manager: " + err.Error())
		os.Exit(1)
	}

	// 1. 取快照后连续更新，快照读到的值不变。
	s := m.Acquire()
	_, _ = m.Update(map[string]any{"host": "beta"})
	_, _ = m.Update(map[string]any{"port": int64(8080)})
	host, _ := s.Get("host")
	ok(host == "alpha" && s.Version() == 1,
		fmt.Sprintf("snapshot v%d still reads host=%v after 2 updates", s.Version(), host))

	// 2. 字段级来源版本。
	sv, _ := m.SourceAt(m.Current(), "port")
	sh, _ := s.SourceVersion("host")
	ok(sv == 3 && sh == 1, fmt.Sprintf("field source: port@v%d, host@v%d", sv, sh))
	s.Release()

	// 3. 切换到历史内容产生更大的新版本号，历史内容仍可取。
	before := m.Current()
	nv, _ := m.Restore(1)
	h1, _ := m.GetAt(1, "host")
	ok(nv > before && h1 == "alpha",
		fmt.Sprintf("restore v1 content -> new v%d > v%d, history intact", nv, before))

	// 4. 有未归还快照时回收被拒，归还后回收成功。
	s2 := m.Acquire()
	_, _ = m.Update(map[string]any{"retries": int64(5)})
	got := m.Collect()
	ok(!slices.Contains(got, nv), fmt.Sprintf("collect kept referenced v%d (got %v)", nv, got))
	s2.Release()
	got = m.Collect()
	ok(slices.Contains(got, nv), fmt.Sprintf("after release collect reclaimed %v", got))

	// 5. 过期快照（指向已回收版本）读取得到可判定错误。
	stale := m.Acquire()
	_, _ = m.Update(map[string]any{"retries": int64(7)})
	stale.Release()
	m.Collect()
	_, err = stale.Get("host")
	ok(errors.Is(err, ontology.ErrVersionReclaimed),
		fmt.Sprintf("stale snapshot read -> %v", err))

	// 6. 已归还快照读取得到另一种可判定错误。
	rel := m.Acquire()
	rel.Release()
	_, err = rel.Get("host")
	ok(errors.Is(err, ontology.ErrSnapshotReleased),
		fmt.Sprintf("released snapshot read -> %v", err))

	// 7. 重复归还无害。
	rel.Release()
	rel.Release()
	ok(m.Leaks().Outstanding == 0, "double release is a harmless no-op")

	// 8. 泄漏报告：未归还数量与指向的版本。
	l1 := m.Acquire()
	l2 := m.Acquire()
	rep := m.Leaks()
	ok(rep.Outstanding == 2 && rep.ByVersion[m.Current()] == 2,
		fmt.Sprintf("leak report: %d outstanding at %v", rep.Outstanding, rep.ByVersion))
	l1.Release()
	l2.Release()

	// 9. 同值更新：来源版本不前进（既定规则）。
	v9, _ := m.Update(map[string]any{"retries": int64(7)})
	src, _ := m.SourceAt(v9, "retries")
	ok(src == v9-1, fmt.Sprintf("same-value update: source stays v%d (new version v%d)", src, v9))

	// 10-12. 三类校验失败，版本号不前进。
	cur := m.Current()
	_, err = m.Update(map[string]any{"ghost": "x"})
	ok(errors.Is(err, ontology.ErrUnknownField), fmt.Sprintf("unknown field -> %v", err))
	_, err = m.Update(map[string]any{"port": "not-an-int"})
	ok(errors.Is(err, ontology.ErrTypeMismatch), fmt.Sprintf("type mismatch -> %v", err))
	_, err = m.Update(map[string]any{"port": int64(70000)})
	ok(errors.Is(err, ontology.ErrOutOfRange), fmt.Sprintf("out of range -> %v", err))
	ok(m.Current() == cur, fmt.Sprintf("failed updates did not advance version (still v%d)", cur))

	fmt.Printf("TOTAL %d checks, %d failed\n", checks, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
