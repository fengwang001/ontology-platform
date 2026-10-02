package refheap

import (
	"fmt"
	"reflect"
	"strings"
)

func formatInit(c, u, m, seed int64) string {
	return fmt.Sprintf("INIT C=%d U=%d M=%d seed=%d", c, u, m, seed)
}

func joinLog(lines []string) string {
	return strings.Join(lines, "\n")
}

func snapshotsEqual(a, b snapshot) bool {
	return a.used == b.used &&
		reflect.DeepEqual(a.objects, b.objects) &&
		reflect.DeepEqual(a.roots, b.roots) &&
		reflect.DeepEqual(a.queues, b.queues) &&
		reflect.DeepEqual(a.finQ, b.finQ)
}

func dumpSnap(s snapshot) string {
	return fmt.Sprintf("used=%d objects=%v roots=%v queues=%v finQ=%v",
		s.used, s.objects, s.roots, s.queues, s.finQ)
}

func mustOKResult(r CollectResult, err error) CollectResult {
	if err != nil {
		panic(err)
	}
	return r
}

func intsEqual(a, b []int64) bool {
	return reflect.DeepEqual(a, b)
}
