# NOTES — 最大最小公平份额（单资源递归水平填充）

## 推导：C=30，A(demand 6) B(12) C(18) D(30)，需求和 66（受约束）
| 步 | 剩余容量 | 剩余任务数 | fair | 本步分配 |
|---|---|---|---|---|
| 起 | 30 | 4 | 30/4=15/2 | 尚未分配 |
| 1 | 30 | 4 | 15/2 | 最小 demand A=6 ≤ 15/2：A 全额，a_A=6；扣后余 24、3 人 |
| 2 | 24 | 3 | 24/3=8 | 最小 demand B=12 > 8：达到最终水位，B=C=D=8，结束 |

最终 **A=6 B=8 C=8 D=8**，sum=30=C，无闲置。

(甲) 错误地按需求比例（分母 66）：A=30·6/66=**30/11**，B=**60/11**，C=**90/11**，D=**150/11**。
对照正确值（以 1/11 计：6=66、8=88）：**A、B 被压低**（30/11<6，60/11<8）；**C、D 超过最大最小份额**（90/11>8，D=150/11≈13.6 ≫ 8，多分最多）。
(乙) 只做一次 fair=30/4=15/2、取 min(demand,15/2) 后不重归一化：A=6，B=C=D=**15/2**，和=57/2，**闲置 3/2**；这 3/2 本应在 A 全额后重算 fair（24/3=8）补给 B、C、D——**各漏 1/2**。
(丙) C=100（需求和 66≤100，不受约束）：正确 A=6 B=12 C=18 D=**30**，**闲置 34**，不补平均。若错误地“总填到同一水位 C/n=25 再按 demand 截断”：D 被错扣成 min(30,25)=**25**（少 5），总发 61、闲置 39。

## 不变量：保证位置 / 钉住的测试函数
1. 与朴素参照一致：`alloc.Allocate`（按 demand 升序单趟、命中水位点即停，mf 精确分数）等价朴素逐步重算；钉于 api `TestAllocateMatchesNaive`，另由 `SelfCheck` 内置核验。
2. 最大最小抬平：`alloc.Allocate` 水位点 s 之后所有未满额者得同一 `(C-S_s)/(m-s)`，满额者 demand≤该水位；钉于 api `TestMaxMinLevel`。
3. 守恒：受约束时水位公式把 C−S_s 全分（和=C），不受约束发全额（和=Σdemand）；比较走 `mf.Frac.Cmp` 交叉相乘；钉于 alloc `TestConservation`。
4. 失败不留痕：`api.New`/`Add` 先做完全部校验再改状态，哨兵 `ErrInvalidCapacity`/`ErrEmptyID`/`ErrDuplicateID`/`ErrNegativeDemand` 互不相同；钉于 api `TestRejectedOpsLeaveNoTrace`。
复杂度：非导出 `alloc.examined` 记录本次填充考察任务数（s 个全额 +1 个水位点，不扫全员）；钉于 alloc `TestExaminedCounterBounded`（m=100…10000 循环，断言恒为 k+1）。
并发：api 互斥锁包住内部 allocator，复制后排序；钉于 api `TestConcurrentAdd`（WaitGroup，无 sleep，对照顺序 Add）。
