// Package medschedule 是住院护理给药时间表系统。
//
// 入口：
//
//	sys, _ := medschedule.NewSystem(300)                 // W=300 秒
//	sys.RegisterDrug(0, "青霉素", "β内酰胺", 8*3600)     // 目录
//	sys.SetAllergy(0, "P01", "β内酰胺", true)            // 过敏
//	sys.CreateOrder(t0, Spec{Kind: "interval", ...})    // 医嘱
//	sys.Administer(now, orderID)                        // 按时给药
//	sys.Refuse(now, orderID)                            // 拒服
//	sys.MakeUp(now, orderID)                            // 漏给补给
//	sys.AdministerPRN(now, orderID)                     // 必要时给药
//	sys.StopOrder(now, orderID)                         // 停嘱
//	sys.ReplaceOrder(now, oldID, spec)                  // 改嘱（原子）
//	pts, _ := sys.QueryPatient(now, "P01", lo, hi)      // 区间查询
//
// 错误以 *Error 返回，用 errors.As 取 Code 区分；错误码见 errors.go。
package medschedule
