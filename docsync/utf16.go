package docsync

import "unicode/utf8"

// utf16Len returns the number of UTF-16 code units in s.
func utf16Len(s string) int {
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

// utf16PrefixLen returns the UTF-16 length of s[0:cutByte].
// cutByte must be a valid byte boundary of s.
func utf16PrefixLen(s string, cutByte int) int {
	n := 0
	for i := 0; i < cutByte; {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return n
}

// classifyUTF16Offset categorizes a UTF-16 offset within s:
//   - 0: a valid boundary (start of a code unit, or end of string)
//   - 1: beyond the end (offset > length, or negative)
//   - 2: between the two code units of an astral-plane character
func classifyUTF16Offset(s string, u16 int) (status int, length int) {
	if u16 < 0 {
		return 1, 0
	}
	cur := 0
	for i := 0; i < len(s); {
		if cur == u16 {
			return 0, cur + utf16Len(s[i:])
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		width := 1
		if r >= 0x10000 {
			width = 2
		}
		if width == 2 && u16 == cur+1 {
			return 2, cur
		}
		cur += width
		i += size
	}
	if u16 == cur {
		return 0, cur
	}
	return 1, cur
}
