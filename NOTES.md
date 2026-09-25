# ontology-481 物化 join 索引 — 推导与不变量

## 八步分步表（pairs = join 对完整集合）

| # | 操作 | R/S 变化 | 本步后 join 对 | JoinSize |
|---|---|---|---|---|
| 1 | SetR(1,10) | R:1→10 | ∅ | 0 |
| 2 | SetR(2,10) | R:2→10 | ∅ | 0 |
| 3 | AddS(10,100) | S[10]={100} | (1,100),(2,100) | 2 |
| 4 | AddS(10,200) | S[10]={100,200} | (1,100),(2,100),(1,200),(2,200) | 4 |
| 5 | DelS(10,100) | S[10]={200} | (1,200),(2,200) | 2 |
| 6 | SetR(1,20) | R:1→20 | (2,200) | 1 |
| 7 | AddS(20,300) | S[20]={300} | (2,200),(1,300) | 2 |
| 8 | DelR(2) | 删 R:2 | (1,300) | 1 |

- (甲) 第5步正确 JoinSize=2。若 DelS 只删首个匹配 a：错成 3，残留另一条 (a,100)（按 a 升序枚举则残留 (2,100)）。
- (乙) 第6步正确 JoinSize=1；不移旧对则错成 2。第7步后 Join(1) 正确为 [300]，错实现为 [200 300]（多出悬空 (1,200)）。
- (丙) 第8步正确 JoinSize=1；不级联则错成 2，残留悬空对 (2,200)。

## 四条不变量的保证位置与钉住测试

1. 与批量重算一致：jidx/jidx.go 的 BindR/UnbindR/AddC/DelC 增量维护 byA、byB、size；api/api.go SelfCheck 内置八步逐步与扫 R×S 重算比对。测试 TestSelfCheck、TestRandomSequences。
2. 双向完整（(a,c) 存在 iff R[a]=b 且 c∈S[b]）：jidx/jidx.go 每次成对增删 byA[a] 与 byB[b]，空集合即时清理；api 写操作先改 rel 再同步 jidx。测试 TestSelfCheck、TestRandomSequences。
3. 变更精确：jidx/jidx.go UnbindR 只清该 a、DelC 经 byB[b] 扇出到全部匹配 a、RebindR 先删旧 b 全部对再建新 b（新旧相同直接返回）。测试 TestSelfCheck、TestDelSFanoutScans、TestDelRChecksZero。
4. 失败不留痕：api/api.go 所有变更先校验（AddS 判重、DelR/DelS 判存在），拒绝在触碰任何状态前返回哨兵错误。测试 TestSelfCheck、TestRandomSequences。
