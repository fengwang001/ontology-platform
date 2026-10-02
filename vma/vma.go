package vma

// VMA flags bits accepted by Mmap.
const (
	FlagFixed     = 1
	FlagNoReplace = 2
	FlagGrowsDown = 4
)

// VMA is a half-open page interval [Start, End).
type VMA struct {
	Start     int64
	End       int64
	Perm      int
	GrowsDown bool
	File      int64 // 0 means anonymous
	Off       int64 // start page offset within the file
}

// Pages returns the length in pages.
func (v VMA) Pages() int64 { return v.End - v.Start }

func (v VMA) anonymous() bool { return v.File == 0 }

// compatible reports whether adjacent a (lower) and b (higher) merge.
func compatible(a, b VMA) bool {
	if a.End != b.Start || a.Perm != b.Perm || a.GrowsDown != b.GrowsDown {
		return false
	}
	if a.File == 0 && b.File == 0 {
		return true
	}
	if a.File == 0 || b.File == 0 || a.File != b.File {
		return false
	}
	return b.Off == a.Off+(a.End-a.Start)
}
