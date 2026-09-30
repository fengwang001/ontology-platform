package anomaly

import (
	"fmt"
	"strings"
)

// 隔离等级。
const (
	LevelNone = "无"
	LevelPL1  = "PL-1"
	LevelPL2  = "PL-2"
	LevelPL2P = "PL-2+"
	LevelPL3  = "PL-3"
)

// 异常类别。
const (
	CatG0        = "G0"
	CatG1a       = "G1a"
	CatG1b       = "G1b"
	CatG1c       = "G1c"
	CatGSingle   = "G-single"
	CatG2        = "G2"
	CatNoAnomaly = "PL-3"
)

func levelOf(category string) string {
	switch category {
	case CatG0:
		return LevelNone
	case CatG1a, CatG1b, CatG1c:
		return LevelPL1
	case CatGSingle:
		return LevelPL2
	case CatG2:
		return LevelPL2P
	default:
		return LevelPL3
	}
}

func cycleReason(c *Cycle) string {
	parts := make([]string, len(c.Txns))
	for i, t := range c.Txns {
		next := c.Txns[(i+1)%len(c.Txns)]
		parts[i] = fmt.Sprintf("T%d --%s--> T%d", t, edgeName(c.Edges[i]), next)
	}
	return "见证环 " + strings.Join(parts, " , ")
}

// Analyze 对一份多版本事务历史做隔离异常判定。
// 该函数及其调用的全部代码只操作调用内新建的数据结构，没有任何
// 可变共享状态，因此可被任意并发调用；相同语义的输入（事务与操作
// 排列不同）必然得到完全相同的类别、等级与见证环。
func Analyze(h History) Result {
	m, reject := validate(h)
	if reject != "" {
		return Result{Accepted: false, Reject: reject}
	}

	g, bad := buildGraph(h, m)

	// 类别按固定顺序只报第一个命中的。
	if w := g.findWitness(cycleSpec{plain: true, useMask: EdgeWW}); w != nil {
		return okResult(CatG0, w, cycleReason(w))
	}
	if len(bad) > 0 && bad[0].category == CatG1a {
		b := bad[0]
		return okResult(CatG1a, nil, fmt.Sprintf(
			"已提交事务 T%d 在键 %q 读到已中止事务 T%d 写的版本 (%d,%d)",
			b.reader, b.key, b.writer, b.version[0], b.version[1]))
	}
	if len(bad) > 0 && bad[0].category == CatG1b {
		b := bad[0]
		return okResult(CatG1b, nil, fmt.Sprintf(
			"已提交事务 T%d 在键 %q 读到事务 T%d 的非最后一次写版本 (%d,%d)",
			b.reader, b.key, b.writer, b.version[0], b.version[1]))
	}
	if w := g.findWitness(cycleSpec{plain: true, useMask: EdgeWW | EdgeWR}); w != nil {
		return okResult(CatG1c, w, cycleReason(w))
	}
	if w := g.findWitness(cycleSpec{exactOne: true}); w != nil {
		return okResult(CatGSingle, w, cycleReason(w))
	}
	if w := g.findWitness(cycleSpec{exactOne: false}); w != nil {
		return okResult(CatG2, w, cycleReason(w))
	}

	return Result{
		Accepted: true,
		Category: CatNoAnomaly,
		Level:    LevelPL3,
		Reason:   "无 G0/G1a/G1b/G1c/G-single/G2 命中",
	}
}

func okResult(category string, w *Cycle, reason string) Result {
	return Result{
		Accepted: true,
		Category: category,
		Level:    levelOf(category),
		Witness:  w,
		Reason:   reason,
	}
}
