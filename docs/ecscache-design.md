# ECS 递归解析缓存设计说明

包路径：`ontology/ecscache`。实现一个支持客户端子网（ECS，EDNS Client
Subnet）扩展的递归解析缓存：同一名称与记录类型可按应答声明的适用范围
分别缓存多份结果；查询时按客户端地址选取适用条目；同源前缀的并发未
命中合并为一次上游查询。时钟与上游解析器均由调用方注入。

## API 一览

- `New(k, clock, upstream)`：构造缓存。`k` 为同名字同类型下的条目数上
  限；`clock func() time.Time` 为注入时钟；`upstream Resolver` 为注入
  的上游解析器。
- `(*Cache).Resolve(ctx, Query) (Result, error)`：解析一次查询。
- `Query{Name, Type, ClientAddress, SourcePrefixLen}`：名字、记录类型、
  客户端地址（4 或 16 字节）、发往上游时声明的源前缀长度（0 表示不
  暴露地址）。
- `Response{Result, TTL, Scope}`：上游应答，含结果（正常记录集 /
  NXDomain / NoData）、生存时间（秒，0–604800）与适用范围前缀长度。
- 错误：`ErrInvalidArgument`（参数非法）优先于 `ErrUpstream`（上游失
  败，含上游应答非法）。

## 关键语义决策

1. **名字规范化**：比较前去掉一个末尾的点并转为小写；发往上游的名字
   也是规范化后的形式。
2. **前缀掩码**：地址在长度之外的低位一律清零后使用（源前缀、缓存键、
   覆盖判定均如此），不报错。
3. **有效适用范围**：缓存键中的范围取 `min(应答声明范围, 源前缀长度)`。
   声明范围为零表示对所有客户端适用。
4. **有效期**：条目在 `[写入时刻, 写入时刻+TTL)` 内有效，到期时刻本身
   已失效（左闭右开）；TTL=0 的应答不缓存。
5. **命中规则**：在同名字、同类型、同地址族的未过期条目中选取前缀覆
   盖客户端地址者，多个取范围最长者；较长范围条目过期后自然回落到较
   短范围的未过期条目（因为判定只看未过期条目）。
6. **否定结果**：与正常结果同规则缓存；缓存键唯一决定一个条目，同键
   写入整体覆盖并重新计时，因此否定与正常结果天然不并存。
7. **容量**：同名字同类型（跨地址族）条目数上限为 K；超限时淘汰剩余
   有效期最短者，并列取范围更长者，再并列取写入更早者（用单调递增的
   写入序号判定）。总条目数不设上限；过期条目在查询与写入路径上被惰
   性清除，永不被当作有效。
8. **合并**：合并键为（规范化名字、类型、地址族、源前缀长度、按源前
   缀清零后的地址）。代表 goroutine 在锁外调用上游，等待者阻塞在组
   的 `done` 通道上。上游失败时所有等待者收到 `ErrUpstream`，不写缓
   存。
9. **未覆盖等待者的重新查询**：等待者是否被共享应答覆盖，用上游**声
   明的**（未钳制）范围与代表客户端的完整地址判定。声明范围不超过源
   前缀时，同组等待者必然全部被覆盖；声明范围更长时，地址落在范围之
   外的等待者重新走完整查询流程：
   - 若结果可缓存（TTL>0）：应答已按源前缀长度钳制写入缓存，重新查
     询必然命中该条目——这与串行执行（先来者写缓存、后来者命中）完
     全等价，因此不会多出一倍上游流量；
   - 若 TTL=0：结果不可缓存，重新查询会真正再次访问上游。
   另设上限 8 次尝试，防御“持续返回不覆盖范围且 TTL=0”的病态上游造
   成活锁，达到上限返回最近一次应答。
10. **错误优先级**：参数校验先于一切缓存与上游操作，因此参数非法永
    远优先于上游失败；被拒绝的查询不改变缓存。上游应答非法（TTL 或
    范围越界、类别非法）按上游失败处理且不写缓存。

## 数据结构

```
buckets: map[(name, type)] → map[(family, scope, maskedPrefix)] → entry   // 每桶 ≤ K
groups:  map[(name, type, family, sourceLen, maskedSourcePrefix)] → group // 进行中的合并查询
```

单把互斥锁保护全部状态；上游调用在锁外进行。线性化点：缓存命中在锁
内完成查找的时刻；合并组的所有成员（含代表）在代表于锁内“写缓存 +
关闭 done 通道 + 摘除组”这一原子步骤处线性化。因此并发调用的结果等
价于某个串行顺序。

## 命中开销与总条目数无关的论证

命中路径：一次 `map` 哈希查找定位桶（均摊 O(1)，仅随名字长度变化），
桶内至多 K 个条目，逐条做 O(地址字节数) 的前缀比较，总计
O(K × 地址位数/8)。不遍历任何全局结构，与缓存总条目数无关。

可验证的证明：`TestHitCostIndependentOfTotalEntries` 在总条目数
1e3 / 1e4 / 1e5 下测量同一热键的命中耗时并打印对照表（宽松断言：100
倍数据量下耗时不超 10 倍）；`BenchmarkHit` 提供相同维度的基准。本地
一次运行结果（linux/arm64）：

```
total_entries=1000    hit_latency=263.5 ns/op
total_entries=10000   hit_latency=265.5 ns/op
total_entries=100000  hit_latency=162.4 ns/op
BenchmarkHit/total=1000    166.5 ns/op
BenchmarkHit/total=10000   173.7 ns/op
BenchmarkHit/total=100000  164.6 ns/op
```

## 被放弃的方案

- **全局前缀 trie（最长前缀匹配）**：查找成本随 trie 深度与全局结构变
  化，且需求允许 O(K) 判定；K 通常很小，桶内线性扫描更简单、更易证
  明复杂度上界。
- **定时器 / 最小堆主动过期**：引入额外并发组件与堆维护成本；需求只
  要求过期条目不被当作有效，惰性清除（查询与写入时）已充分。
- **整把锁包住上游调用**：实现最简单，但会阻塞所有无关查询，且等待
  者退化为串行；改为“锁内建组、锁外调用、通道广播”的 singleflight
  结构。
- **声明范围大于源前缀时不缓存**：曾考虑以避免放大适用范围，但需求
  明确要求“按源前缀长度处理”，且钳制缓存与串行语义自洽（见决策 9），
  故放弃。
- **上游只收到掩码后的地址**：上游需要代表客户端的完整地址才能计算
  自己的适用范围（真实解析器也知道客户端完整 IP，ECS 源前缀只是对
  外声明的粒度），故 `Request` 携带完整地址加源前缀长度。
- **按 (名字, 类型, 地址族) 分桶计容量**：需求原文为“同名字同类型下
  条目数上限为 K”，未含地址族，故容量桶不含地址族（两族共享 K）。

## 本地验证方法

```bash
# 全部测试（含竞态检测）
go test -race ./ecscache/

# 随机序列对照（1200 组 × 25 步，-v 打印每步输入/输出/判定依据）
go test -run TestModelComparison -v ./ecscache/

# 命中开销与总条目数无关的验证
go test -run TestHitCostIndependentOfTotalEntries -v ./ecscache/
go test -run xxx -bench BenchmarkHit ./ecscache/

# 静态检查
go vet ./ecscache/ && gofmt -l ecscache/
```

测试覆盖：恰到期边界（`TestTTLExactExpiryBoundary`）、长范围过期回落
（`TestFallbackToShorterScopeAfterExpiry`）、范围为零与大于源前缀
（`TestScopeZeroCoversAllAddresses`、`TestScopeLongerThanSourcePrefixIsClamped`）、
地址族隔离（`TestAddressFamilyIsolation`）、否定/正常互相覆盖
（`TestNegativeAndPositiveOverwriteViaQuery`、`TestStoreOverwriteSameKey`）、
淘汰并列次序（`TestEvictionShortestRemainingTTL`、
`TestEvictionTieBreakLongerScope`、`TestEvictionTieBreakEarlierWrite`）、
并发合并与不覆盖者重新查询（`TestConcurrentMergeSingleUpstreamCall`、
`TestConcurrentNonCoveredWaiterRequeries`、
`TestConcurrentNonCoveredWaiterHitsClampedEntry`）、拒绝次序
（`TestInvalidArgumentRejectedFirst`）、并发串行等价性
（`TestConcurrentSerialEquivalence`），以及与独立朴素模型对照的 1200
组随机序列（`TestModelComparison`）。
