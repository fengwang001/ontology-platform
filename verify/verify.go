// Package verify 提供段损坏检测与倒排不变量自检。
package verify

import (
	"fmt"
	"sort"

	"ontology/segment"
)

// CheckFile 检测段文件是否损坏；损坏时返回四类可判定错误之一
// （ErrHeaderIncomplete / ErrDictIncomplete / ErrPostingsIncomplete / ErrCRCMismatch）。
func CheckFile(path string) error {
	_, err := segment.Read(path)
	return err
}

// CheckInvariants 自检内存段的倒排不变量：
// 词典有序、每词文档号严格升序、每篇文档内位置严格升序、无悬挂链。
func CheckInvariants(s *segment.Segment) error {
	if !sort.StringsAreSorted(s.Terms) {
		return fmt.Errorf("verify: dictionary not sorted")
	}
	for _, term := range s.Terms {
		l, ok := s.Lookup(term)
		if !ok {
			return fmt.Errorf("verify: term %q has no posting list (dangling)", term)
		}
		for i, p := range l {
			if i > 0 && l[i-1].Doc >= p.Doc {
				return fmt.Errorf("verify: term %q doc ids not ascending at %d", term, i)
			}
			for j := 1; j < len(p.Pos); j++ {
				if p.Pos[j-1] >= p.Pos[j] {
					return fmt.Errorf("verify: term %q doc %d positions not ascending", term, p.Doc)
				}
			}
		}
	}
	return nil
}

// CheckRecovered 校验从截断段恢复的前缀是否自洽。
func CheckRecovered(s *segment.Segment) error {
	return CheckInvariants(s)
}
