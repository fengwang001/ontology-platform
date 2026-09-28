package watermark

import (
	"fmt"
	"strconv"
	"strings"
)

// Logger 接收组件的判定日志。标准库 *log.Logger 天然满足该接口；
// 测试可注入自定义实现以断言日志内容。
type Logger interface {
	Printf(format string, args ...any)
}

// logDecision 输出一次操作的输入、各分区空闲判定、候选集合、
// 合并水位的前后值与推进依据。调用方需持有 m.mu（日志只读 decision，
// 不触碰可变状态）。
func (m *Merger) logDecision(d decision) {
	if m.logger == nil {
		return
	}

	var b strings.Builder

	// 输入
	fmt.Fprintf(&b, "watermark op=%s at=%d", d.op, d.at)
	if d.hasReport {
		fmt.Fprintf(&b, " partition=%d reported=%d", d.partition, d.reported)
	}
	if d.prevClockSet {
		fmt.Fprintf(&b, " prev_clock=%d", d.prevClock)
	} else {
		b.WriteString(" prev_clock=unset")
	}

	// 各分区判定依据：elapsed / idle / watermark
	b.WriteString(" | partitions=[")
	for i := range m.partitions {
		if i > 0 {
			b.WriteByte(' ')
		}
		p := &m.partitions[i]
		fmt.Fprintf(&b, "{p=%d wm=%s reported=%s elapsed=%s idle=%t}",
			i,
			wmText(p),
			boolText(p.hasWatermark),
			elapsedText(p, d.elapsed[i]),
			d.idle[i])
	}
	b.WriteByte(']')
	fmt.Fprintf(&b, " threshold=%d", m.idleThreshold)

	// 候选集合与候选最小值
	if d.hasCandidate {
		fmt.Fprintf(&b, " | active_partitions=%s candidate_min=%d",
			intsText(d.candidates), d.minCandidate)
	} else {
		fmt.Fprintf(&b, " | active_partitions=%s candidate_min=n/a", intsText(d.candidates))
	}

	// 合并水位结果与依据
	fmt.Fprintf(&b, " | merged %d -> %d advanced=%t (%s)",
		d.prevMerged, d.newMerged, d.advanced, d.reason)

	m.logger.Printf("%s", b.String())
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func wmText(p *partitionState) string {
	if !p.hasWatermark {
		return "n/a"
	}
	return strconv.FormatInt(int64(p.watermark), 10)
}

func elapsedText(p *partitionState, e Time) string {
	if !p.hasWatermark {
		return "n/a"
	}
	return strconv.FormatInt(int64(e), 10)
}

func intsText(xs []int) string {
	if len(xs) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range xs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(x))
	}
	b.WriteByte(']')
	return b.String()
}
