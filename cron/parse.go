package cron

import (
	"strconv"
	"strings"
)

// fieldBounds 给出五个字段的最小、最大取值以及取值个数。
var fieldBounds = [fieldCount][3]int{
	{0, 59, 60}, // 分
	{0, 23, 24}, // 时
	{1, 31, 31}, // 日
	{1, 12, 12}, // 月
	{0, 6, 7},   // 周（0 为星期日，7 非法）
}

// Parse 解析五个以单个空格分隔的字段。
// 按字段、项的出现次序逐项检查：语法、取值越界、范围反向、步长非法，
// 返回第一个违规对应的错误。
func Parse(spec string) (out *cronSpec, err error) {
	fields := strings.Split(spec, " ")
	// 先判字段个数；随后按字段、项的次序逐项检查。
	if len(fields) != fieldCount {
		return nil, ErrFieldCount
	}
	out = &cronSpec{}
	for fi := 0; fi < fieldCount; fi++ {
		out.fields[fi], err = parseField(fields[fi], fi)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func parseField(text string, fi int) (fieldSpec, error) {
	lo, hi := fieldBounds[fi][0], fieldBounds[fi][1]
	spec := fieldSpec{values: make([]bool, hi+1)}
	if text == "" {
		return fieldSpec{}, ErrSyntax
	}
	items := strings.Split(text, ",")
	for _, item := range items {
		loVal, hiVal, step, wildcard, err := parseItem(item, lo, hi)
		if err != nil {
			return fieldSpec{}, err
		}
		if wildcard {
			spec.wildcard = true
		}
		for v := loVal; v <= hiVal; v += step {
			spec.values[v] = true
		}
	}
	return spec, nil
}

// parseItem 解析单个逗号项，返回实际范围、步长与是否通配。
func parseItem(item string, lo, hi int) (start, end, step int, wildcard bool, err error) {
	if item == "" {
		return 0, 0, 0, false, ErrSyntax
	}
	// 合法性字符与结构检查：首字符决定是否带通配标志。
	wild := item[0] == '*'
	runes := []byte(item)
	seenDash := false
	seenSlash := false
	for i, ch := range runes {
		switch ch {
		case '*':
			if i != 0 {
				return 0, 0, 0, false, ErrSyntax
			}
		case '-':
			if seenDash || i == 0 || i == len(runes)-1 {
				return 0, 0, 0, false, ErrSyntax
			}
			if runes[i+1] == '/' || runes[i+1] == '-' {
				return 0, 0, 0, false, ErrSyntax
			}
			seenDash = true
		case '/':
			if seenSlash || i == 0 || i == len(runes)-1 {
				return 0, 0, 0, false, ErrSyntax
			}
			if runes[i-1] == '-' || runes[i-1] == '/' {
				return 0, 0, 0, false, ErrSyntax
			}
			seenSlash = true
		default:
			if ch < '0' || ch > '9' {
				return 0, 0, 0, false, ErrSyntax
			}
		}
	}
	if wild && seenDash {
		return 0, 0, 0, false, ErrSyntax
	}

	rangePart, stepPart := item, ""
	if seenSlash {
		idx := strings.IndexByte(item, '/')
		rangePart, stepPart = item[:idx], item[idx+1:]
		if strings.IndexByte(stepPart, '/') >= 0 || stepPart == "" {
			return 0, 0, 0, false, ErrSyntax
		}
	}

	if wild {
		// * 或 */s：范围恒为整个字段。
		if rangePart != "*" {
			return 0, 0, 0, false, ErrSyntax
		}
		start, end = lo, hi
	} else {
		var lowText, highText string
		if seenDash {
			idx := strings.IndexByte(rangePart, '-')
			lowText, highText = rangePart[:idx], rangePart[idx+1:]
		} else if seenSlash {
			// a/s 等价于 a 到字段上限步长 s。
			lowText = rangePart
		} else {
			lowText, highText = rangePart, rangePart
		}
		if lowText == "" || (highText == "" && !seenSlash) {
			return 0, 0, 0, false, ErrSyntax
		}
		lowVal, err := strconv.Atoi(lowText)
		if err != nil || lowVal < lo || lowVal > hi {
			return 0, 0, 0, false, ErrRange
		}
		highVal, err := strconv.Atoi(highText)
		if highText == "" {
			highVal = hi
		} else if err != nil || highVal < lo || highVal > hi {
			return 0, 0, 0, false, ErrRange
		}
		if lowVal > highVal {
			return 0, 0, 0, false, ErrReversed
		}
		start, end = lowVal, highVal
	}

	// 步长检查排在取值越界与范围反向之后。
	step = 1
	if seenSlash {
		s, perr := strconv.Atoi(stepPart)
		err = perr
		if err != nil || s < 1 || s > hi-lo+1 {
			return 0, 0, 0, false, ErrStep
		}
		step = s
	}
	return start, end, step, wild, nil
}
