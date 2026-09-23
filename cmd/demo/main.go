package main

import (
	"fmt"
	"os"
)

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		os.Exit(1)
	}
}

func main() {
	checkPublishAndRead()
	checkOldVersionHeld()
	checkReclaimAfterLastRelease()
	checkNoPileUp()
	checkSameContentIncrements()
	checkAcquireBeforePublish()
	checkThreeReleaseErrors()
	checkActiveLimitRejected()
	checkSelfCheckAfterRejections()
	checkAccessCountTwoSizes()
	checkConcurrentConsistency()
	fmt.Println("ALL OK")
}

func checkPublishAndRead()           { report("发布并读取", false) }
func checkOldVersionHeld()           { report("持旧发布时旧内容不变", false) }
func checkReclaimAfterLastRelease()  { report("最后持有者归还后回收", false) }
func checkNoPileUp()                 { report("无人持有的非当前版不堆积", false) }
func checkSameContentIncrements()    { report("内容相同也递增版本", false) }
func checkAcquireBeforePublish()     { report("尚无快照时返回可判定错误", false) }
func checkThreeReleaseErrors()       { report("三类归还错误可区分", false) }
func checkActiveLimitRejected()      { report("超上限被拒且无半发布", false) }
func checkSelfCheckAfterRejections() { report("被拒前后自检均通过", false) }
func checkAccessCountTwoSizes()      { report("两档访问记录数不随规模增长", false) }
func checkConcurrentConsistency()    { report("并发下内容与版本始终自洽", false) }
