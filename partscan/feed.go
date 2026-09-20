package partscan

// Feed 喂入任意长度（含 0 长度）的一块字节，返回本次新完成的段。
//
// 扫描器内部只保存“当前未完成段”：buf 的前 partLen 字节是已确认段内容，
// 其后可能挂着一个正在形成的分隔符（由 KMP 状态 q 与 step 状态机跟踪）。
// 分隔符被否定时挂起字节退回段内容；被确认时吐出段并清空缓冲。所有状态
// 跨块保留，因此任意切块方式都与逐字节喂入整条流等价。
func (s *Scanner) Feed(p []byte) ([][]byte, error) {
	switch s.phase {
	case phaseDone:
		if len(p) > 0 {
			return nil, ErrAfterClose
		}
		return nil, nil
	case phaseError:
		return nil, s.term
	}

	if len(p) == 0 {
		return nil, nil
	}

	chunk := cloneBytes(p) // 与调用方内存隔离

	if s.phase == phasePreamble {
		used, err := s.consumePreamble(chunk)
		if err != nil {
			return nil, err
		}
		if s.phase != phaseBody {
			return nil, nil // preamble 仍可能被切在下一块
		}
		chunk = chunk[used:]
	}

	return s.scanBody(chunk), nil
}

// preamble 为流开头必须出现的 "--<boundary>\r\n"。
func preambleOf(boundary string) []byte {
	return append(append([]byte("--"), boundary...), '\r', '\n')
}

// consumePreamble 增量确认开头的 --boundary\r\n，返回本块消费掉的字节数。
// 块结束时若已消费字节仍是 preamble 的前缀则等待下一块；一旦出现不一致
// 字节立即返回 ErrNoPreamble。
func (s *Scanner) consumePreamble(chunk []byte) (int, error) {
	pre := preambleOf(s.boundary)
	used := 0
	for _, c := range chunk {
		if len(s.pre) == len(pre) {
			break
		}
		if c != pre[len(s.pre)] {
			s.phase = phaseError
			s.term = ErrNoPreamble
			return 0, ErrNoPreamble
		}
		s.pre = append(s.pre, c)
		used++
	}
	if len(s.pre) == len(pre) {
		s.phase = phaseBody
	}
	return used, nil
}

func cloneBytes(p []byte) []byte {
	if len(p) == 0 {
		return []byte{}
	}
	c := make([]byte, len(p))
	copy(c, p)
	return c
}
