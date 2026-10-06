// locker-demo 演示完整业务闭环并打印每个操作的输入、输出与判定依据。
// 运行：go run ./cmd/locker-demo
package main

import (
	"fmt"
	"os"

	"ontology/locker"
)

func main() {
	cells := []locker.Cell{
		{ID: 1, Size: locker.SizeSmall},
		{ID: 2, Size: locker.SizeSmall},
		{ID: 3, Size: locker.SizeMedium},
		{ID: 4, Size: locker.SizeLarge},
	}
	cfg := locker.Config{
		FreeStorage:   10, // 秒：免费保管 10s
		BillingPeriod: 5,  // 之后每 5s 一个计费周期
		FeePerPeriod:  2,  // 每周期 2 元
		FeeCap:        9,  // 单件封顶 9 元
		MaxStorage:    100,
		CodeCooldown:  20,
		CodeCount:     100,
	}
	cab, err := locker.NewCabinet(cells, cfg, locker.NewTextLogger(os.Stdout))
	if err != nil {
		panic(err)
	}

	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}

	fmt.Println("== 存件：小件依次占小格口 1、2 ==")
	r1, err := cab.Deposit(0, "SF1001", locker.SizeSmall, "13800001234")
	must(err)
	_, err = cab.Deposit(1, "SF1002", locker.SizeSmall, "13800002345")
	must(err)

	fmt.Println("== 第三个小件：小格口满，差一档升级中格口 3 ==")
	_, err = cab.Deposit(2, "SF1003", locker.SizeSmall, "13800003456")
	must(err)

	fmt.Println("== 错误手机号连续三次 -> 锁定（时钟不推进）==")
	_, _ = cab.Pickup(2, r1.Code, "13800009999")
	_, _ = cab.Pickup(2, r1.Code, "13800008888")
	_, _ = cab.Pickup(2, r1.Code, "13800007777")
	_, err = cab.Pickup(2, r1.Code, "13800001234")
	fmt.Println("正确号码取件结果（期望已锁定）:", err)

	fmt.Println("== 运营解锁后正常取件 ==")
	must(cab.Unlock(3, "SF1001"))
	tracking, err := cab.Pickup(3, r1.Code, "13800001234")
	must(err)
	fmt.Println("取走快件:", tracking)

	fmt.Println("== 超时件只能回收：t=1+100=101 ==")
	err = cab.Recycle(100, "SF1002") // 差一秒
	fmt.Println("提前回收（期望 ErrNotTimedOut）:", err)
	must(cab.Recycle(101, "SF1002"))
	fmt.Println("SF1002 已回收，格口释放")
}
