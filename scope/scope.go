package scope

func Covers(granted, needed string) bool {
	return granted == needed || (len(needed) > len(granted) && needed[:len(granted)] == granted && needed[len(granted)] == ':')
}

func FirstMissing(granted, needed []string) (string, bool) {
	for _, want := range needed {
		covered := false
		for _, have := range granted {
			if Covers(have, want) {
				covered = true
				break
			}
		}
		if !covered {
			return want, true
		}
	}
	return "", false
}

func High(needed []string) bool {
	for _, item := range needed {
		last := item
		for i := len(item) - 1; i >= 0; i-- {
			if item[i] == ':' {
				last = item[i+1:]
				break
			}
		}
		if last == "write" || last == "admin" {
			return true
		}
	}
	return false
}
