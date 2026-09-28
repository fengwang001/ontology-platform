package txnset

// Parse 解析事务集合文本，返回规范化后的集合。
// 任何非法输入都使整个解析失败，不会返回部分结果。
func Parse(text string) (*Set, error) {
	return nil, nil
}

// MustParse 同 Parse，但解析失败时 panic，仅供测试与常量初始化使用。
func MustParse(text string) *Set {
	s, err := Parse(text)
	if err != nil {
		panic(err)
	}
	return s
}

// String 输出集合的规范文本：来源按字典序、区间按升序，
// 单点写作 "N"，区间写作 "N-M"，区间间以 "," 分隔，来源间以 ";" 分隔。
// 空集合输出空字符串。
func (s *Set) String() string {
	return ""
}
