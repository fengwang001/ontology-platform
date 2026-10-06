package smartlocker

import "strconv"

func formatCodeNumber(number uint64) string {
	return strconv.FormatUint(number, 10)
}

func codeNumber(code string) uint64 {
	number, _ := strconv.ParseUint(code, 10, 64)
	return number
}
