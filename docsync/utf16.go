package docsync

import (
	"errors"
	"unicode/utf16"
	"unicode/utf8"
)

// 位置合法性错误，可区分越界与代理对中间。
var (
	// ErrOutOfBounds 行不存在，或列超过该行 UTF-16 码元数。
	ErrOutOfBounds = errors.New("docsync: position out of bounds")
	// ErrInsideSurrogatePair 列落在增补平面字符的两个 UTF-16 码元之间。
	ErrInsideSurrogatePair = errors.New("docsync: position inside surrogate pair")
	// ErrInvalidRange 范围起点大于终点。
	ErrInvalidRange = errors.New("docsync: invalid range (start after end)")
	// ErrStaleVersion 基版本与当前版本不一致。
	ErrStaleVersion = errors.New("docsync: stale base version")
	// ErrOverlappingEdits 同一组变更内的编辑范围重叠。
	ErrOverlappingEdits = errors.New("docsync: overlapping edits")
)

// Position 是（行，列）；列以 UTF-16 码元计，均从 0 起。
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range 是 [Start, End) 的半开范围，两端都须合法且 Start <= End。
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Severity 是诊断严重度。
type Severity int

const (
	SeverityError       Severity = 1
	SeverityWarning     Severity = 2
	SeverityInformation Severity = 3
	SeverityHint        Severity = 4
)

// Edit 是一次变更中的单个编辑：把 Range 替换为 Text。
// Range 为空是插入，Text 为空是删除。
type Edit struct {
	Range Range  `json:"range"`
	Text  string `json:"text"`
}

// Diagnostic 是一条已发布诊断（不含版本字段；失效版本在失效清单中）。
type Diagnostic struct {
	Range    Range    `json:"range"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// deadDiagnostic 记录一条已失效诊断与其失效时版本。
type deadDiagnostic struct {
	diag          Diagnostic
	sequence      int // 登记次序（全局唯一）
	bornVersion   int // 登记时版本
	failedVersion int // 失效时版本
	start, end    int // 失效瞬间（变更前坐标）的 UTF-16 偏移，仅用于日志/测试
}

// len16 返回字符串的 UTF-16 码元长度。
func len16(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// runeLen16 返回单个 rune 的 UTF-16 码元长度。
func runeLen16(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// line16Len 返回一行文本（不含换行符）的 UTF-16 码元长度。
func line16Len(line string) int { return len16(line) }

// validateColumn 校验某一行内的列：0 <= col <= 行码元数；
// 返回 ErrOutOfBounds / ErrInsideSurrogatePair / nil。
func validateColumn(line string, col int) error {
	if col < 0 {
		return ErrOutOfBounds
	}
	cu := 0
	for _, r := range line {
		w := runeLen16(r)
		if col < cu+w {
			// col == cu 合法（字符前）；col == cu+1 且 w==2 为代理对中间；
			// col == cu+w 合法（字符后），由下一轮或结尾判定。
			if w == 2 && col == cu+1 {
				return ErrInsideSurrogatePair
			}
			break
		}
		cu += w
	}
	if col > cu {
		return ErrOutOfBounds
	}
	return nil
}

// offsetOfRuneIndex 返回第 idx 个 rune 起始处的 UTF-16 偏移（idx 可为 rune 数，即行尾）。
func offset16AtRune(line string, idx int) int {
	cu := 0
	i := 0
	for _, r := range line {
		if i >= idx {
			break
		}
		cu += runeLen16(r)
		i++
	}
	return cu
}

// columnToRuneBoundary 给定合法行内 UTF-16 列，返回其字节偏移与码元偏移。
// col 必须已经通过 validateColumn。
func columnToByteOffset(line string, col int) int {
	cu := 0
	bo := 0
	for _, r := range line {
		if cu >= col {
			break
		}
		w := utf8.RuneLen(r)
		bo += w
		cu += runeLen16(r)
	}
	return bo
}

// countNewlines 统计字符串中的 '\n' 数（回车是普通字符，不特殊处理）。
func countNewlines(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			n++
		}
	}
	return n
}

// appendEncoded 仅用于明确语义占位：Go 字符串即 UTF-8，替换文本直接使用。
var _ = utf16.Encode
