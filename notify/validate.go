package notify

const maxEff = int64(1_000_000_000_000)

// validName 校验模板名：1 到 32 个小写字母、数字、点或下划线。
func validName(name string) bool {
	if len(name) < 1 || len(name) > 32 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' {
			continue
		}
		return false
	}
	return true
}

// validVariable 校验变量名：1 到 32 个小写字母、数字或下划线。
func validVariable(v string) bool {
	if len(v) < 1 || len(v) > 32 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			continue
		}
		return false
	}
	return true
}

func validChannel(ch string) bool {
	return ch == "email" || ch == "sms" || ch == "push" || ch == "*"
}

func validConcreteChannel(ch string) bool {
	return ch == "email" || ch == "sms" || ch == "push"
}

func validEff(eff int64) bool { return eff >= 0 && eff <= maxEff }
