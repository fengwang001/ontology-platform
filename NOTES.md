# NOTES — 空闲超时水位

delay=3, timeout=5。Feed：先置 lastEventPT=pt；mark=ts-3，mark<wm 为迟到（严格小于，等号收）。Tick：pt-lastEventPT>=5 触发，wm=max(wm,pt-3)。

| 步 | 操作 | lastPT | 空闲触发 | 迟到丢弃 | 步后 wm |
|---|---|---|---|---|---|
| 1 | Feed a ts10@pt0 | 0 | - | 否 | 7 |
| 2 | Feed b ts20@pt1 | 1 | - | 否 | 17 |
| 3 | Tick pt10 | 1 | 是 (9>=5) | - | max(17,7)=17 |
| 4 | Feed c ts20@pt11 | 11 | - | 否 (17==17 等号收) | 17 |
| 5 | Tick pt12 | 11 | 否 (1<5) | - | 17 |
| 6 | Tick pt30 | 11 | 是 (19>=5) | - | max(17,27)=27 |
| 7 | Feed d ts25@pt31 | 31 | - | 是 (22<27) | 27 |
| 8 | Tick pt40 | 31 | 是 (9>=5) | - | max(27,37)=37 |

最终 wm=37，Dropped=1。
(甲) 空闲直接赋值 wm=pt-3：第 3 步错成 7；正确 max(17,7)=17。
(乙) 迟到判据写成 <=：第 4 步 c 的 17<=17 被误判迟到、Dropped 提前变 1；正确等号边界接受、不丢弃，wm 仍 17。
(丙) 空闲推进用事件时间：第 6 步停在 maxEvtTS-3=17（正确 27），d 因此(22>=17)不再迟到被误收，第 8 步 wm=max(22,25-3)=22；最终错成 22，正确 37。

## 不变量保证（位置 / 钉住的测试）

1. 批量重算一致：api.go 的 SelfCheck 重放八步，取全部事件 mark 与触发 tick mark 的 max；测试 TestBatchRecompute、TestEightSteps。
2. 水位单调：唯一推进点 wtm.Advance 只取 max（wtm.go）；测试 TestMonotonic。
3. 空闲只用处理时间：idle.go Tick 以 pt-lastEventPT 判定、pt-delay 推进，不读事件时间；测试 TestProcessingTimeIdle（第 6 步 wm=27>17）。
4. 失败不留痕：api.go New/Feed/Tick 全部校验通过后才调用 idle，拒绝路径不碰任何状态；测试 TestRejectionLeavesNoTrace（三类哨兵 errors.Is）。

迟到检查计数器 checks 是 idle 包非导出字段，公开接口不暴露，仅同包白盒测试读取：TestLateCheckConstant（m=100/1000/10000，检查数恒为 0 不随 m 增长）。
并发：所有状态访问经 idle.Stream.mu 互斥；测试 TestConcurrentReadersIdentical、TestConcurrentAdvanceMonotonic（go test -race）。
