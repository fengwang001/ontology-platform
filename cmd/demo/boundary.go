package main

import (
	"ontology/multipart"
	"ontology/serve"
)

// 演示需要逐字节对照同一响应在不同切分下的输出，因此使用确定性边界串。
// 生产路径（serve 包默认）仍使用 crypto/rand 生成，见 multipart.ChooseBoundary。
func init() {
	serve.SetBoundaryChooser(func(content []byte, attempts int) (string, error) {
		candidate := "DEMOBND00000000000000000000000000"
		for i := 0; i < attempts; i++ {
			c := candidate
			if i > 0 {
				c = "DEMOBND" + string(rune('A'+i)) + "0000000000000000000000"
			}
			if !multipart.ContainsBoundary(content, c) {
				return c, nil
			}
		}
		return "", serve.ErrBoundaryAttempts
	})
}
