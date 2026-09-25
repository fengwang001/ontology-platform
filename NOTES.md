# 延迟物化引擎 NOTES

## 一、七步推导（a=2,b=3,c=4；d=a+b，e=d*2，f=c+e）

| 步 | 操作 | 触发计算（依赖序） | 返回 | 复用缓存 |
|---|---|---|---|---|
| 1 | Get(e) | d=5 → e=10 | e=10 | 无 |
| 2 | Get(d) | 无 | d=5 | d |
| 3 | Get(f) | f=14 | f=14 | e |
| 4 | Set(a,5) | 无；失效 d,e,f | — | — |
| 5 | Get(d) | d=8 | d=8 | 无 |
| 6 | Get(f) | e=16 → f=20 | f=20 | d |
| 7 | Get(e) | 无 | e=16 | e |

**(甲)** 惰性 New 计算 0 个派生列；急切 New 计算 3 个（d,e,f，差 3），第 1 步 Get(e) 急切计算 0 个、惰性计算 2 个（d,e，差 2）。
**(乙)** Set(a,5) 应失效 d,e,f；若只失效直接依赖 d，则 e,f 保留陈旧缓存，第 6 步 Get(f)=14，正确为 20。
**(丙)** 不做缓存时第 2 步 Get(d) 重算 d，合计 1 次；正确为 0 次。

## 二、不变量落点（代码位置 / 钉住测试）

1. 朴素一致：`api/api.go` `SelfCheck` 内 `naive` 闭包即时重算并逐值对照；测试 `TestNaiveAgreement`、`TestSevenSteps`。
2. 缓存一致：`mat/mat.go` `getLocked` 命中 valid 缓存即返回，仅 `Set` 经闭包失效；测试 `TestCacheConsistency`、`TestChainCounter`。
3. 失效正确：`dep/dep.go` `Build` 建反图，`DependentsClosure` BFS 算传递闭包，`mat/mat.go` `Set` 遍历闭包失效；测试 `TestInvalidation`、`TestChainCounter`。
4. 失败不留痕：`dep.Build` 全部校验通过才构图，`mat` 的 `Get`/`Set` 先判错后改状态；测试 `TestRejectedAtomic`、`TestErrors`。
