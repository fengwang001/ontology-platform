# NOTES

排序键：score 降序，同分 id 升序。K=3 七步表（操作后完整有序序列 -> TopK）：
1. Add(m,10) : [m10]                     | [m10]
2. Add(a,10) : [a10 m10]                 | [a10 m10]
3. Add(z,20) : [z20 a10 m10]             | [z20 a10 m10]
4. Add(y,20) : [y20 z20 a10 m10]         | [y20 z20 a10]
5. Add(b,5)  : [y20 z20 a10 m10 b5]      | [y20 z20 a10]
6. Remove(z) : [y20 a10 m10 b5]          | [y20 a10 m10]
7. Add(k,10) : [y20 a10 k10 m10 b5]      | [y20 a10 k10]
(甲) 第4步 id 升序 y<z，y 在 z 前，TopK=[y20 z20 a10]；错按插入先后则 z 在 y 前，错成 [z20 y20 a10]。
(乙) 第6步 m10 补位；若被挤出者直接丢弃，m 已在第4步丢失，只剩 [y20 a10]，缺 m10。
(丙) 第7步正确留 a、k，排除 m：[y20 a10 k10]；按插入先后（10 分者 m 第1步、a 第2步、k 第7步）则留 m、a，错排 k。
     故同分必须有确定次级键：否则同一状态的答案随插入历史而变，名次与前缀不确定、不可复现。

不变量 -> 代码保证位置 -> 钉住它的测试：
1. 与朴素参照一致：topk.go 的 rebalance（惰性 purges + top/bot 堆顶交换）在每次 Add/Remove 后恢复分区；TestNaiveReference 每步比对。
2. 撤回正确补位：rebalance 中 top 不足 K 即从 bot 弹出最优者补入；TestRemoveBackfill。
3. 并列确定、前缀一致：ord.go 的 Less（score 降、id 升）唯一确定名次；TestTieOrderAndPrefix（含 k'<=K 前缀断言）。
4. 失败不留痕：api.go 的 New/Add/Remove 全部先校验、返回哨兵错误后才改状态；TestRejectedOpsNoTrace。
