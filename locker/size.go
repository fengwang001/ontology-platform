package locker

// Size 是格口/快件规格：小、中、大三档，序值满足 小 < 中 < 大。
type Size uint8

const (
	SizeSmall  Size = 0
	SizeMedium Size = 1
	SizeLarge  Size = 2
	numSizes   Size = 3
)

func (s Size) valid() bool { return s <= SizeLarge }

// TrackingNo 运单号。
type TrackingNo string

// Phone 收件人手机号；系统只校验并使用其末四位。
type Phone string

// lastFour 返回手机号后四位；不足四位或含非数字字符时 ok=false。
func (p Phone) lastFour() (string, bool) {
	s := string(p)
	if len(s) < 4 {
		return "", false
	}
	tail := s[len(s)-4:]
	for _, r := range tail {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return tail, true
}
