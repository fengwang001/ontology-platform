# NOTES：断点续传累加器推导与不变量

## 七步分步表（初始 cp=-1, sum[k]=0, pending={}）

| 步 | 操作 | pending 中 offset 集合 | cp | sum[k] |
|---|---|---|---|---|
| 1 | Apply(k,0,+10) | {0} | -1 | 0 |
| 2 | Apply(k,2,+20) | {0,2} | -1 | 0 |
| 3 | Apply(k,3,+30) | {0,2,3} | -1 | 0 |
| 4 | Commit() | {2,3} | 0 | 10 |
| 5 | Apply(k,2,+20) 重复，幂等跳过 | {2,3} | 0 | 10 |
| 6 | Apply(k,1,+40) | {1,2,3} | 0 | 10 |
| 7 | Commit() | {} | 3 | 100 |

(甲) 第4步后正确值 cp=0、sum=10。若错把 cp 设为 pending 最大 offset：cp 错成 3、sum 错成 60（0,2,3 全计入、缺口 1 被跳过）；随后 Apply(k,1,+40) 因 1<=cp=3 被误判「已持久化」而丢弃，最终 sum 错成 60（正确 100）。
(乙) 只查 offset<=cp、不查 pending，且 pending 用追加切片：第5步 offset 2 被当新事件再追加，pending=[2,3,2]；第6步后 [2,3,2,1]；第7步 Commit 对前缀 1,2,3 的每个匹配条目都累加：10+40+20+20+30=120，sum 错成 120（正确 100，offset 2 的 +20 算了两次）。
(丙) 第4步后 Restore：sum=10、cp=0 保留，pending{2,3} 丢失。源端从 cp+1=1 重投 offset 1,2,3（+40,+20,+30），重放后 sum=100。若 cp 未持久化复位为 -1：源端从 0 起多投 offset 0（+10），它被判为新事件再次累加，sum 错成 110。

## 四条不变量：保证位置与钉住它的测试

1. 与朴素参照一致：`acc.Commit` 只折叠 `ckpt.Advance` 返回的连续前缀（acc.go）；测试 `TestNaiveReference`（随机交错对拍朴素扫描）。
2. 连续性（cp 不越缺口）：`ckpt.Advance` 遇第一个缺口即停（ckpt.go）；测试 `TestSevenStepTrace`、`TestNaiveReference`。
3. 幂等/精确恢复：`ckpt.Decide` 把 offset<=cp 判 Persisted、在途判 Inflight，均跳过；`acc.Restore` 只清 pending（acc.go）；测试 `TestIdempotentAndRestore`。
4. 失败不留痕：`api.Apply` 先校验空 Key/负 offset 再入 acc，`acc.Apply` 先查上限再插入，拒绝路径零写（api.go/acc.go）；测试 `TestRejections`（三哨兵互不相同、状态不变、可继续用）。

复杂度：`acc.Apply` 用 map 判重，非导出计数器 `probes` 记录检查条目数；白盒测试 `acc.TestProbeBound` 断言 m=100..10000 时 probes<=1。并发：`acc` 内 RWMutex；测试 `api.TestConcurrentApply`。
