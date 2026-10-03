package mig

import "fmt"

func probeVerdict(moved, probed int) string {
	if moved > 0 && probed > 2*moved {
		return fmt.Sprintf("违反探测预算: 探测%d > 2*搬迁%d", probed, moved)
	}
	return fmt.Sprintf("探测%d <= 2*搬迁%d", probed, moved)
}
