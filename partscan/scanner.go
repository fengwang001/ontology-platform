package partscan

// Scanner 是分隔符驱动的分段扫描器。
//
// 流的整体形态：
//
//	--<boundary>\r\n
//	<part1>\r\n--<boundary>\r\n
//	<part2>\r\n--<boundary>--
//
// 数据可以按任意大小的块调用 Feed 喂入；分隔符本身也可能被切在两个块
// 之间。扫描器只负责按 boundary 定界，不解析段内的任何内容。
type Scanner struct {
	boundary string

	match prefixMatcher // 匹配 "\r\n--"+boundary

	pre     []byte // 开头 preamble "--"+boundary+"\r\n" 的增量暂存
	buf     []byte // 当前未完成段（含可能挂起的分隔符后缀）
	q       int    // KMP 当前匹配到的模式前缀长度
	partLen int    // buf 中已确认属于段内容的字节数
	step    step   // 正文 lookahead 状态

	phase phase
	term  error // 终止态下固定返回的错误
}

type phase uint8

const (
	phasePreamble phase = iota // 正在确认开头的 --boundary\r\n
	phaseBody                  // 正在扫描各段
	phaseDone                  // 已见到结束分隔符
	phaseError                 // 已进入错误终止态
)

// New 创建一个以 boundary 定界的扫描器。
func New(boundary string) *Scanner {
	return &Scanner{
		boundary: boundary,
		match:    newPrefixMatcher("\r\n--" + boundary),
	}
}

// Close 表示流结束；未见到结束分隔符时返回 ErrIncomplete。
func (s *Scanner) Close() error {
	switch s.phase {
	case phaseDone:
		return nil
	case phaseError:
		return s.term
	default:
		s.phase = phaseError
		s.term = ErrIncomplete
		return ErrIncomplete
	}
}

// Done 报告是否已经见到结束分隔符。
func (s *Scanner) Done() bool { return s.phase == phaseDone }

// Pending 返回当前尚未成段的暂存字节数。
func (s *Scanner) Pending() int {
	if s.phase == phasePreamble {
		return len(s.pre)
	}
	if s.phase != phaseBody {
		return 0
	}
	return s.partLen
}
