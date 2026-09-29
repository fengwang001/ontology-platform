// 命令 server 演示 CDC 驱动的维表查询缓存：更新入队、事件延迟投递、
// 两阶段读取回填在栅栏前被拒、删除事件使缓存失效，最终与直读源头一致。
package main

import (
	"fmt"

	"ontology/dimcache"
)

func main() {
	m := dimcache.New(128, dimcache.NewLogger(logWriter{}))

	v1, _ := m.Update("user-1", "alice")
	query(m, "user-1")

	// 先取得读取令牌，再让更新事件先到达，模拟回填与失效交错。
	tok, _, _ := m.BeginRead("user-1")
	v2, _ := m.Update("user-1", "alice-v2")
	must(m.Emit(dimcache.Event{Key: "user-1", Version: v2, Value: "alice-v2"}))
	delivered, err := m.DeliverNext()
	must(err)
	fmt.Printf("delivered=%v fence=%d\n", delivered, m.Fence("user-1"))

	if err := m.Backfill(tok); err != nil {
		fmt.Printf("stale backfill rejected as expected: %v\n", err)
	}
	query(m, "user-1")

	// 删除事件与负缓存。
	v3, _ := m.Delete("user-1")
	must(m.Emit(dimcache.Event{Key: "user-1", Version: v3, Deleted: true}))
	_, _ = m.DeliverNext()
	query(m, "user-1")

	// 延迟到达的更旧 v1 事件被忽略。
	must(m.Emit(dimcache.Event{Key: "user-1", Version: v1, Value: "alice"}))
	_, _ = m.DeliverNext()

	fmt.Printf("quiescent=%v source-reads=%d\n", m.Quiescent(), m.SourceReads())
}

func query(m *dimcache.Manager, key string) {
	val, found, err := m.Query(key)
	if err != nil {
		panic(err)
	}
	if found {
		fmt.Printf("query %s -> FOUND %q\n", key, val)
		return
	}
	fmt.Printf("query %s -> NOT FOUND (negative cache capable)\n", key)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	fmt.Print("  LOG ", string(p))
	return len(p), nil
}
