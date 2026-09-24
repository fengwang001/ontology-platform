# 双时钟水位 NOTES（L=5）

## 八步分步表（迟到判定取本事件到达前的 ETW）

| # | 操作 | maxEt | ETW | PTW | 判定 | LateCount |
|---|------|-------|-----|-----|------|-----------|
| 1 | Ingest(10,a) | 10 | 5 | -inf | 按时（首事件） | 0 |
| 2 | Heartbeat(100) | 10 | 5 | 100 | -（只推 PTW） | 0 |
| 3 | Ingest(8,b) | 10 | 5 | 100 | 按时（8>5） | 0 |
| 4 | Ingest(20,c) | 20 | 15 | 100 | 按时 | 0 |
| 5 | Ingest(14,d) | 20 | 15 | 100 | 迟到（14<=15） | 1 |
| 6 | Heartbeat(200) | 20 | 15 | 200 | -（只推 PTW） | 1 |
| 7 | Ingest(16,e) | 20 | 15 | 200 | 按时（16>15） | 1 |
| 8 | Ingest(15,f) | 20 | 15 | 200 | 迟到（15<=15） | 2 |

终态：View=[(8,b),(10,a),(16,e),(20,c)]，LateCount=2，maxEt=20，ETW=15，PTW=200。

## 三问

- (甲) 判据误写成 `et < ETW`：第 8 步 f(15==ETW) 被误判**按时** → LateCount 错成 1，View 多出 (15,f)。
- (乙) 误用 PTW 判定（`et <= PTW`）：第 7 步 e(16<=200) 被误判**迟到** → e 被丢弃，View 少了 (16,e)，LateCount 错成 2（第 8 步 f 也会跟着错）。
- (丙) 心跳抬 ETW=PTW-L：第 2 步后 ETW 错成 95；第 3 步 b(8<=95) 被误判**迟到**丢弃。违反不变量 3（Heartbeat 绝不改变 ETW/View/LateCount），进而违反不变量 1（与朴素重放不再一致）。

## 四条不变量：保证位置 → 钉住测试

1. 与朴素重放一致：dual.Ingest 先调 etime.Ingest 判定、按结果决定是否入集合，先判后改且全程单锁 → api 包测试 TestNaiveReplay（随机交错序列对拍）。
2. 判定只看 ETW：etime.go 的 Ingest 内唯一判据 `et <= w.etw`，PTW 不在判定路径上 → TestEightSteps（第 7 步 e 在 PTW=200 下仍按时）。
3. 两水位单调且独立：etime.Ingest 仅当 et>maxEt 时抬 maxEt/ETW；dual.Heartbeat 只写 ptw 且拒绝回退 → TestMonotonicity。
4. 失败不留痕：dual.New/Ingest/Heartbeat 一律先校验后改状态，返回互不相同哨兵错误 → TestRejectsStateUnchanged。
另：etime 的非导出计数器 scanCnt 由 etime 包内测试 TestScanCountConstant 钉住（m=100/1000/10000 档均为 0，不随 m 增长）。
