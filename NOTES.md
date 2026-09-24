# 物化状态引用计数 GC：推导与不变量

## 八步表（v1=10, v2=20；h1/h2 指向 v1，h3 指向 v2）

| # | 操作 | v1 refs | v2 refs | 存活集合 | AliveCount |
|---|---|---|---|---|---|
| 1 | P(10) | 1 | - | {v1} | 1 |
| 2 | h1=A() | 2 | - | {v1} | 1 |
| 3 | h2=A() | 3 | - | {v1} | 1 |
| 4 | P(20) | 2 | 1 | {v1,v2} | 2 |
| 5 | h3=A() | 2 | 2 | {v1,v2} | 2 |
| 6 | R(h1) | 1 | 2 | {v1,v2} | 2 |
| 7 | R(h2) | 0→回收 | 2 | {v2} | 1 |
| 8 | h1.Get() | 0(已回收) | 2 | {v2} | 1，返回 ErrUseAfterFree |

(甲) 第 4 步后 v1 refs=2（store 引用 -1，h1/h2 各持 1）。若 Acquire 不 +1，则 refs 错成 0，P(20) 内 v1 被立即回收，h1/h2 悬空。
(乙) 第 7 步后 AliveCount=1（v1 归零立即删除，仅 v2）。延迟回收会错成 2。
(丙) 必须返回哨兵 ErrUseAfterFree 且状态不变；若不检测、回缓存值，会错返回 10。

## 四条不变量（保证位置 / 钉住的测试）

1. 与朴素重算一致：存活=当前版本 1 + 仍被未释放句柄引用的历史版本；store.go 的 Publish/Acquire/Release 只做 ±1 与归零即删。测试 TestNaiveRecompute。
2. 共享不误回收：Acquire 立即 AddRef（store.go:Acquire），refs>0 绝不移出存活集合，Get 恒返回发布值。测试 TestSharedNotReclaimed。
3. 零引用立即回收：ver.go:ReleaseOne 判零，store.go:Release/Publish 同步 delete 存活集合并置 freed。测试 TestImmediateReclaim、TestReleaseCheckedIsO1。
4. 失败不留痕：互斥锁内先全部校验、后改状态，四类哨兵错误定义于 store.go 顶部、api.go 转发。测试 TestRejectedOpsLeaveNoTrace；并发由 TestConcurrentAcquireRelease（go test -race）钉住。
