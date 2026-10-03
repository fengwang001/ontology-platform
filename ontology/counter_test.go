package ontology

import (
	"fmt"
	"testing"
)

// TestSubjectExamCounter 用非导出计数器证明：主题回退的候选查找只考察
// 规范化主题相同的线程，考察数与线程总数无关。
//
// 布局：总线程数 T 中仅 S 个线程根主题为 "T"，其余 T-S 个为互异主题。
// 随后插入一封规范化主题为 "T" 的回复邮件（时间窗允许全部命中），
// 考察数必须恰为 S（<= 同主题线程数），在 T=100 与 T=10000 两档保持一致。
func TestSubjectExamCounter(t *testing.T) {
	measure := func(totalThreads, sameSubject int) int {
		m, err := New(1_000_000_000, totalThreads+10)
		if err != nil {
			t.Fatal(err)
		}
		// 同主题 S 个线程，ts 全部相同，用于制造并列。
		for i := 0; i < sameSubject; i++ {
			id := fmt.Sprintf("t-%05d", i)
			if _, err := m.Add(id, nil, "", "T", 100); err != nil {
				t.Fatal(err)
			}
		}
		// 其余线程各自互异主题。
		for i := 0; i < totalThreads-sameSubject; i++ {
			id := fmt.Sprintf("o-%05d", i)
			subj := fmt.Sprintf("other-%05d", i)
			if _, err := m.Add(id, nil, "", subj, 100); err != nil {
				t.Fatal(err)
			}
		}
		m.resetSubjectExamCount()
		res, err := m.Add("reply-zzz", nil, "", "Re: T", 100)
		if err != nil {
			t.Fatal(err)
		}
		if res.Way != "subject" {
			t.Fatalf("way = %s, want subject (threads=%d, same=%d)", res.Way, totalThreads, sameSubject)
		}
		return m.subjectExamCount()
	}

	const same = 20
	c100 := measure(100, same)
	c10000 := measure(10000, same)
	t.Logf("候选考察数: 总线程=100 -> %d；总线程=10000 -> %d（同主题线程数均为 %d）",
		c100, c10000, same)
	if c100 != same || c10000 != same {
		t.Fatalf("examines = %d, %d; want both %d (bounded by same-subject threads, independent of total)",
			c100, c10000, same)
	}

	// 再对照：同主题线程数翻倍，考察数随之翻倍；线程总数不变。
	c40 := measure(10000, 40)
	t.Logf("候选考察数: 总线程=10000、同主题=40 -> %d", c40)
	if c40 != 40 {
		t.Fatalf("examines = %d, want 40", c40)
	}
}
