package partscan

// 正文扫描的状态机：
//
//	stepRun    常规扫描，q 是 KMP 当前匹配长度
//	stepLook1  已凑齐完整模式 "\r\n--b"，等待其后第一个字节
//	stepClose  第一个字节是 '-'，等待第二个 '-'（结束分隔符）
//	stepSepN   第一个字节是 '\r'，等待 '\n'（段分隔符）
type step uint8

const (
	stepRun step = iota
	stepLook1
	stepClose
	stepSepN
)

// scanBody 逐字节把 chunk 并入当前未完成段。
func (s *Scanner) scanBody(chunk []byte) [][]byte {
	pat := s.match.length()
	var out [][]byte

	for _, c := range chunk {
		s.buf = append(s.buf, c)

		switch s.step {
		case stepLook1:
			switch {
			case c == '-':
				s.step = stepClose
				continue
			case c == '\r':
				s.step = stepSepN
				continue
			default:
				// 偶然匹配：模式与该字节都是正文。模式以 boundary
				// 结尾，其前缀不可能是新的模式前缀，q 归零后喂当前字节。
				s.q = s.match.update(0, c)
				s.partLen = len(s.buf)
				s.step = stepRun
			}
		case stepClose:
			if c == '-' {
				out = append(out, cloneBytes(s.buf[:s.partLen]))
				s.buf, s.q, s.partLen = s.buf[:0], 0, 0
				s.step = stepRun
				s.phase = phaseDone
				return out
			}
			// "-X"：模式连同 "-X" 都是正文，退回常规扫描。
			s.q = s.match.update(0, '-')
			s.q = s.match.update(s.q, c)
			s.partLen = len(s.buf)
			s.step = stepRun
		case stepSepN:
			if c == '\n' {
				out = append(out, cloneBytes(s.buf[:s.partLen]))
				s.buf, s.q, s.partLen = s.buf[:0], 0, 0
				s.step = stepRun
				continue
			}
			// "\rX"：模式连同 "\rX" 都是正文，退回常规扫描。
			s.q = s.match.update(0, '\r')
			s.q = s.match.update(s.q, c)
			s.partLen = len(s.buf)
			s.step = stepRun
		default: // stepRun
			s.q = s.match.update(s.q, c)
			if s.q == pat {
				s.partLen = len(s.buf) - pat
				s.step = stepLook1
			} else {
				s.partLen = len(s.buf)
			}
		}
	}

	return out
}
