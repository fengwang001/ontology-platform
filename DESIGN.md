# 设计: 分片扇出查询的部分失败合并器

模型: 聚合查询扇出到 N 个分片; 每个分片返回记录 (ID, 分数), 并可给出本分片 TopK 上界 bound。成功集合 S, 缺失集合 M, 真实值 T, 已观测值 V。

## 1. Count
T = V + C_M, C_M >= 0 => V <= T。V 是下界, 标注 `>= V`。

## 2. Sum
T = V + S_M, S_M >= 0(分数非负) => V <= T。下界, 标注 `>= V`。

## 3. Min
缺失分片可能含更小值: T <= V。单边界方向为上界, 标注 `<= V`。

## 4. Max
缺失分片可能含更大值: T >= V。单边界方向为下界, 标注 `>= V`。
(把 Min 写成 `>=`、Max 写成 `<=` 是最常见的方向错误。)

## 5. TopK 与可信前缀
令 B = sum_{m in M} bound[m], 即缺失分片可能贡献的最大单项上界之和(取和是对"取 max"的严格保守上界, 结论恒成立)。
对合并榜单第 j 名 x(分数 v_j): 若 v_j > B, 则任何缺失条目的分数都 <= B < v_j, 无法越过 x, x 必然入选真实 TopK。
榜单按分数降序、并列按 ID 升序。可信前缀长度 p = 最长连续前缀中每条都满足 v > B 的长度, p <= K。
- p = K: 整个 TopK 可信(缺失上界很小 => B 小 => p=K)。
- p = 0: 一个位置都不能保证(缺失上界很大 => B 大 => p=0)。
前缀条目入选确定, 其彼此排名由成功数据决定, 同样确定。

全部成功(M 为空): 五种聚合均 Exact, 不降级。

## 分片状态与错误
状态枚举: OK / Empty / Timeout / Corrupt / Duplicate / Failed。Empty(成功 0 条)与失败可区分; 全 Empty != 全失败。
可判定错误: ErrNoShard(零分片), ErrAllShardsFailed(全部失败)。

## 资源约束
并发: 容量 C 的信号量; 非导出计数器记历史峰值 maxFlight <= C, 提供只读访问器。
截止: 父 ctx 到点后立即停止派发(新发起数为 0), 取消全部在飞子 ctx, Wait 在截止后很短窗口内返回。

## 故障注入
超时: 分片阻塞至 ctx.Done => Timeout。损坏: 声称条数 != 实际记录数 => Corrupt, 坏数据不入合并。
重复: 同一分片回调两次 => 仅取第一次, 状态 Duplicate, 计数不翻倍。全失败: 无成功分片 => ErrAllShardsFailed。

## 并发与边界
fanout 按 ShardID 排序汇总; TopK 分数降序 + ID 升序; 缺失清单按 ShardID 排序 => 任意到达顺序逐字节相同。空串 ShardID 合法。
K > 总条目数 => 返回现有全部; 记录缺字段按零值参与, 不致失败。

包: shard / fanout / combine / confidence / report + cmd/demo。仅标准库, 状态在进程内存。
