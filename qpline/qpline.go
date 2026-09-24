// Package qpline decides byte escaping and soft line breaks for one
// quoted-printable logical line.
package qpline

const hexUpper = "0123456789ABCDEF"

// AppendLine appends the encoded form of line. trailing reports whether the
// logical line ends immediately after line; trailing spaces and tabs are then
// escaped. column is the current encoded column, allowing a logical line that
// already consumed space before a call to continue.
func AppendLine(dst, line []byte, trailing bool, column int) []byte {
	trailingSpaces := 0
	if trailing {
		for trailingSpaces < len(line) {
			b := line[len(line)-1-trailingSpaces]
			if b != ' ' && b != '\t' {
				break
			}
			trailingSpaces++
		}
	}

	cut := len(line) - trailingSpaces
	for i := 0; i < len(line); i++ {
		if i < cut && line[i] != '=' && line[i] >= 33 && line[i] <= 126 {
			if column+1 > 76 {
				dst = append(dst, '=', '\r', '\n')
				column = 0
			}
			dst = append(dst, line[i])
			column++
			continue
		}

		if i < cut && (line[i] == ' ' || line[i] == '\t') {
			if column+1 > 76 {
				dst = append(dst, '=', '\r', '\n')
				column = 0
			}
			dst = append(dst, line[i])
			column++
			continue
		}

		if column+3 > 76 {
			dst = append(dst, '=', '\r', '\n')
			column = 0
		}
		dst = append(dst, '=', hexUpper[line[i]>>4], hexUpper[line[i]&0x0f])
		column += 3
	}
	return dst
}
