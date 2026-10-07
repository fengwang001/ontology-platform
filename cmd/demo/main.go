// demo 演示属性索引重建、增量应用、独立复核与判定日志打印。
// 运行：go run ./cmd/demo
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	p := ontology.NewPlatform(ontology.NewJSONLogger(logWriter{}))

	p.DeclareIndex("Employee", "city")
	p.Put("Employee", "alice", "city", "BJ")
	p.Put("Employee", "bob", "city", "SH")

	// 发起重建（记录基准点），期间仍有并发写入到达。
	h, _ := p.StartRebuild("Employee", "city")
	p.Put("Employee", "carol", "city", "SZ") // 增量写入
	p.Put("Employee", "alice", "city", "GZ") // 同对象增量覆盖

	rec, _ := h.Complete()
	fmt.Printf("rebuild B=%d C=%d entries=%d digest=%s\n\n",
		rec.BaselineSeq, rec.CompleteSeq, len(rec.Entries), rec.Digest)

	// 独立复核（仅凭审计记录 + 对象当前状态）。
	rep := p.Verify("Employee", "city")
	fmt.Printf("verify consistent=%v history_reads=%d cell_reads=%d\n\n",
		rep.Consistent, rep.HistoryReads, rep.CellReads)

	// 人为注入条目级错误，复核仍应独立发现。
	p.InjectCorruption("Employee", "city", "bob", "XX")
	rep2 := p.Verify("Employee", "city")
	for _, m := range rep2.Mismatches {
		fmt.Printf("MISMATCH object=%s index_value=%s kind=%s detail=%s\n",
			m.Object, m.Value, m.Kind, m.Detail)
	}
}

type logWriter struct{}

func (logWriter) Write(b []byte) (int, error) {
	fmt.Printf("[log] %s", b)
	return len(b), nil
}
