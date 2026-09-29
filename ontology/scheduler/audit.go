package scheduler

import (
	"fmt"
	"io"
	"strings"
)

// DumpAuditLog 将每个已接受事务的输入、深度、轮次与判定依据写入 w。
// maxParallel 必须为正整数；日志内容对同一事务序列与并行度逐字段一致。
func (s *Scheduler) DumpAuditLog(w io.Writer, maxParallel int) error {
	plans, err := s.Plans(maxParallel)
	if err != nil {
		return err
	}
	txns := s.snapshot()
	bySeq := make(map[int]*txn, len(txns))
	for _, t := range txns {
		bySeq[t.seq] = t
	}
	var b strings.Builder
	fmt.Fprintf(&b, "audit log: %d transaction(s), maxParallel=%d\n", len(plans), maxParallel)
	for _, p := range plans {
		t := bySeq[p.Seq]
		fmt.Fprintf(&b, "tx %d: readKeys=[%s] writeKeys=[%s] depth=%d round=%d dependsOn=[%s] reason=%q\n",
			p.Seq,
			strings.Join(t.readKeys, ","),
			strings.Join(t.writeKeys, ","),
			p.Depth, p.Round,
			joinInts(p.DependsOn),
			p.Reason)
	}
	_, err = io.WriteString(w, b.String())
	return err
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("%d", x)
	}
	return strings.Join(parts, ",")
}
