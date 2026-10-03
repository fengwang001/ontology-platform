package suppression

import "strings"

// Normalize 按固定顺序规范化邮件地址：
//  1. 全部 ASCII 字母转小写；
//  2. 域名 googlemail.com 改为 gmail.com；
//  3. 本地部分从第一个 + 起截断（截后为空则非法）；
//  4. 域名为 gmail.com 时去掉本地部分全部 .（去后为空则非法）。
//
// 输入地址须恰含一个 @，本地部分与域名非空，域名至少含一个 . 且无空标签，
// 不含不大于 0x20 的字节与 0x7f，总长不超过 254。
func Normalize(addr string) (string, error) {
	if len(addr) == 0 || len(addr) > 254 {
		return "", ErrInvalidAddress
	}
	for i := 0; i < len(addr); i++ {
		if addr[i] <= 0x20 || addr[i] == 0x7f {
			return "", ErrInvalidAddress
		}
	}
	if strings.Count(addr, "@") != 1 {
		return "", ErrInvalidAddress
	}
	at := strings.IndexByte(addr, '@')
	local, domain := addr[:at], addr[at+1:]
	if local == "" || domain == "" {
		return "", ErrInvalidAddress
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return "", ErrInvalidAddress
	}
	for _, label := range labels {
		if label == "" {
			return "", ErrInvalidAddress
		}
	}

	local = strings.ToLower(local)
	domain = strings.ToLower(domain)

	if domain == "googlemail.com" {
		domain = "gmail.com"
	}

	if plus := strings.IndexByte(local, '+'); plus >= 0 {
		local = local[:plus]
		if local == "" {
			return "", ErrInvalidAddress
		}
	}

	if domain == "gmail.com" {
		local = strings.ReplaceAll(local, ".", "")
		if local == "" {
			return "", ErrInvalidAddress
		}
	}

	return local + "@" + domain, nil
}
