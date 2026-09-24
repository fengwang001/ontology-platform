# NOTES：引用计数回收推导与不变量

## 一、八步推导表（v1=值10，v2=值20；refs 中 s=store 引用，h=句柄引用）

| 步 | 操作 | v1.refs | v2.refs | 存活集合 | AliveCount |
|---|---|---|---|---|---|
| 1 | P(10) | 1(s) | — | {v1} | 1 |
| 2 | h1=A() | 2(s+h1) | — | {v1} | 1 |
| 3 | h2=A() | 3(s+h1+h2) | — | {v1} | 1 |
| 4 | P(20) | 2(h1+h2) | 1(s) | {v1,v2} | 2 |
| 5 | h3=A() | 2 | 2(s+h3) | {v1,v2} | 2 |
| 6 | R(h1) | 1(h2) | 2 | {v1,v2} | 2 |
| 7 | R(h2) | 0→回收 | 2 | {v2} | 1 |
| 8 | h1.Get() | 已回收，报错 | 2 | {v2} | 1 |

- (甲) 第 4 步后 v1.refs=2（h1、h2 两份句柄引用）。若 Acquire 不加计数，v1.refs 会错成 0，v1 在第 4 步被误回收，h1/h2 悬空。
- (乙) 第 7 步后 AliveCount=1。若延迟回收，v1 仍留存活集合，AliveCount 错成 2。
- (丙) 第 8 步正确行为：返回哨兵错误 ErrUseAfterFree（不返回值）。若不检测直接返回缓存值，会错成返回 10。

## 二、四条不变量的保证位置与钉住测试

1. 与朴素重算一致：store 以 map 存存活版本、current 持 store 引用，refs 增减与 Publish/Acquire/Release 一一对应（store/store.go）；测试 TestNaiveConsistency。
2. 共享不误回收：回收唯一判据是 ver.Version.Dec 归零（ver/ver.go），句柄引用期间 refs≥1；测试 TestSharedNotReclaimed。
3. 零引用立即回收：store.release 在 Dec 归零的同一临界区内 delete 存活项；测试 TestImmediateReclaim。
4. 失败不留痕：所有校验（双释、use-after-free、空 Acquire、负 Publish）先于任何状态变更，直接返回哨兵错误；测试 TestFailureNoSideEffect 与 TestFaultInjection。
