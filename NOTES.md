# 空闲超时水位 NOTES

## 第三节推导（delay=3, timeout=5）

| 步 | 操作 | lastEventPT | 空闲触发 | 迟到丢弃 | 本步后水位 |
|---|---|---|---|---|---|
| 1 | Feed a ts=10 pt=0 | 0 | - | 否 | 7 |
| 2 | Feed b ts=20 pt=1 | 1 | - | 否 | 17 |
| 3 | Tick pt=10 | 1 | 是（10-1=9>=5） | - | max(17,7)=17 |
| 4 | Feed c ts=20 pt=11 | 11 | - | 否（17==17 不迟到） | 17 |
| 5 | Tick pt=12 | 11 | 否（12-11=1<5） | - | 17 |
| 6 | Tick pt=30 | 11 | 是（30-11=19>=5） | - | max(17,27)=27 |
| 7 | Feed d ts=25 pt=31 | 31 | - | 是（22<27） | 27 |
| 8 | Tick pt=40 | 31 | 是（40-31=9>=5） | - | max(27,37)=37 |

最终水位 37，丢弃计数 1。

- (甲) 空闲触发不取 max：第 3 步后水位错成 7（从 17 回退）；正确应为 17。
- (乙) 等号算迟到：第 4 步 c（20-3=17==17）会被误判迟到丢弃、Dropped 变 1；正确是接受 c、水位仍 17、不丢。
- (丙) 空闲推进用事件时间：最大事件时间停在 20，第 6、8 步只能算得 20-3=17，最终水位错成 17；正确应为 37。

## 第二节四条不变量

1. 批量一致：idle/idle.go 的 Feed/Tick 只经 wtm.Advance 累积，api/api.go replay 独立重算对比（SelfCheck 内）；测试 TestBatchConsistency。
2. 水位单调：wtm/wtm.go Advance 恒取 max，是唯一推进入口；测试 TestMonotonic。
3. 空闲只用处理时间：wtm.Idle 判 pt−lastEventPT>=timeout，idle.Tick 用 pt−delay 推进；测试 TestIdleUsesProcessingTime。
4. 失败不留痕：api/api.go New/Feed/Tick 全部校验先于任何状态写入；测试 TestFailureLeavesNoTrace。
