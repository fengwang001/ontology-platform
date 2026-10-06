// 演示程序：构造一个合同需量 100kW、窗口 60s、滑差 30s 的场景，
// 上报若干次用电并打印输入、输出与判定依据。
package main

import (
	"log"
	"os"

	"ontology/demand"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags)
	c, err := demand.New(demand.Config{
		ContractKW: 100,
		WindowSec:  60,
		SlipSec:    30,
		MaxPowerKW: 1000,
	}, demand.WithLogger(logger))
	if err != nil {
		log.Fatal(err)
	}

	must(c.AddLoad(0, demand.LoadSpec{ID: 1, RatedKW: 10, Priority: 1, MinOnSec: 30, MinOffSec: 30}))
	must(c.AddLoad(0, demand.LoadSpec{ID: 2, RatedKW: 40, Priority: 2, MinOnSec: 10, MinOffSec: 30}))
	must(c.AddLoad(0, demand.LoadSpec{ID: 3, RatedKW: 40, Priority: 2, MinOnSec: 10, MinOffSec: 30}))

	report(c, 30, 100*30) // 100kW：恰好等于合同需量，不越限
	report(c, 60, 100*30) // 关窗，实测 100kW
	report(c, 70, 140*10) // 跳到 140kW：预测越限
	report(c, 100, 90*30) // 降到 90kW：恢复窗口
	if p := c.Peak(); p != nil {
		log.Printf("历史最高实测需量 %.6fkW @end=%d", p.PowerKW.Float64(), p.EndAt)
	}
}

func report(c *demand.Controller, t, e int64) {
	r, err := c.Report(t, e)
	if err != nil {
		log.Fatalf("上报失败 t=%d: %v", t, err)
	}
	log.Printf("== 上报结果 t=%d 动作数=%d 仍越限=%v", r.At, len(r.Actions), r.StillExceed)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
