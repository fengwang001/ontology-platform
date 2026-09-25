# NOTES

## 八步推导（缓冲按到达序；输出为三元组 (Key,LVal,RVal)）

| 步 | 事件 | 左缓冲 | 右缓冲 | 本步输出 | 累计 |
|---|---|---|---|---|---|
| 1 | R(k,100) | ∅ | (k,100) | 无 | 0 |
| 2 | L(k,1) | (k,1) | (k,100) | (k,1,100) | 1 |
| 3 | L(null,2) | (k,1),(null,2) | (k,100) | 无 | 1 |
| 4 | R(null,200) | (k,1),(null,2) | (k,100),(null,200) | 无 | 1 |
| 5 | R(k,101) | (k,1),(null,2) | (k,100),(null,200),(k,101) | (k,1,101) | 2 |
| 6 | L("",3) | (k,1),(null,2),("",3) | 同第5步 | 无 | 2 |
| 7 | R("",300) | 同第6步 | (k,100),(null,200),(k,101),("",300) | ("",3,300) | 3 |
| 8 | L(k,4) | (k,1),(null,2),("",3),(k,4) | 同第7步 | (k,4,100),(k,4,101) | 5 |

**甲**：指针 `==` 时 `nil==nil` 为真，第4步错输出 `(null,2,200)`；累计由 1 错成 2，全段由 5 错成 6。
**乙**：NULL 归一成 `""` 存储，第6步 L("",3) 错配第4步 R(null,200)，多输出 `("",3,200)`（第7步还会再多 `("",2,300)`）。
**丙**：第8步正确 2 条；按 Key 去重只留最新会丢掉 `(k,4,100)`、仅出 `(k,4,101)`，累计由 5 错成 4。

## 不变量落点

1. 与朴素嵌套循环一致：`join/join.go` 的 `Feed` 按对侧缓冲插入序探测、每行对只由后到达者触发一次；`TestNestedLoopEquivalence` 钉住。
2. NULL 隔离、空串≠NULL：`nkey/nkey.go` 的 `Matches` 任一为 nil 即 false，`""` 走 `*a==*b`；`TestNullIsolation`、`TestEmptyStringDistinctFromNull` 钉住。
3. 多值不去重：`join/join.go` 哈希桶 append 保存全部行位，不覆盖；`TestMultiplicity` 钉住。
4. 失败不留痕：`api/api.go` 的 `New`/`Feed` 所有校验先于任何状态修改；`TestRejectionLeavesNoTrace`、`TestSentinelErrors` 钉住。

复杂度：非导出 `join.lastProbed` 只记本次哈希桶探测行数（非 nil 键=桶大小，nil=0）；`join/join_test.go` 的 `TestProbeCounter` 钉住不随 m 线性增长。
并发：`join/join.go` 互斥锁保护、`api` 的 `Outputs` 返回深拷贝；`TestConcurrentReaders` 钉住；对外自检入口为 `api.(*Joiner).SelfCheck`。
