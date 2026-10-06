# 服务器名称证书选择器 — 设计说明

## 目标

TLS 终结网关依据 SNI（主机名）、客户端 `ClientHello` 中通告的密钥类型与当前
时刻，从可热更新的证书集合中选择一张证书，并区分结果来源（精确 / 通配 /
默认回退）与失败原因（无匹配 / 全部过期 / 密钥类型不支持）。

包路径：`ontology/certselector`

## 领域规则与关键取舍

### 时间区间：左闭右开

证书在 `[NotBefore, NotAfter)` 内有效：`NotBefore <= now < NotAfter`。
因此 `now == NotBefore` 可用、`now == NotAfter` 已失效。入库时要求
`0 <= NotBefore < NotAfter`，空区间与负时刻直接判参数非法。

### 名字规范化

比较前统一 `strings.ToLower`，并去掉恰好一个末尾点（根标签）。规范化后
校验：非空、无空标签（`.`、`a..b`）、无任何空白字符、单标签 ≤ 63 字节、
总长 ≤ 253 字节。客户端传空串表示"未提供名字"，不参与非法校验。

### 通配名

- 合法形态仅为 `*.<至少两个标签>`，即星号必须是完整的最左标签，其后至少
  两个标签（`*.com` 非法，`*.example.com` 合法）。
- 星号出现在其他位置（`a*.com`、`a.*.com`、`*.example.*`）一律非法。
- 匹配语义为"恰好多一个标签"：`*.example.com` 匹配 `a.example.com`，
  不匹配 `example.com`（少）、`a.b.example.com`（多两个）、
  `x.y.example.com`（多三个）。

### 选择优先级

1. 名字非空：先收集精确匹配；只要存在精确匹配（哪怕全部不可用）就只在其中
   选择，绝不再看通配；无精确匹配时才收集通配匹配。
2. 名字为空或没有任何精确/通配匹配：回退默认证书；默认未设置 → 无匹配；
   默认不可用 → 按其自身原因（过期 / 密钥不支持）失败。
3. 候选可用条件：在有效区间内 **且** 密钥类型被客户端支持。
4. 可用候选排序：ECDSA 优先于 RSA；其次 `NotAfter` 更晚；其次 ID 字典序
   更小（确定性并列裁决，避免返回值随 map 迭代顺序漂移）。

### 失败三分类与优先级

- `ErrNoMatch`：没有任何匹配证书，且无默认（或名字为空且无默认）。
- `ErrExpired`：有匹配，但没有一张落在有效区间。
- `ErrUnsupportedKey`：有匹配，且至少一张在有效区间内，但时间有效的证书
  密钥类型客户端都不支持。过期与密钥不支持并存时，报密钥不支持。

错误一律先判参数非法（空支持集、负时间、非法名字/证书字段），再判选择
失败。失败用实现了 `Unwrap()` 的 `*FailureError` 返回，`errors.Is` 可判定
上述哨兵错误；被拒绝的操作（冲突、不存在、非法参数）不改动任何状态。

### 集合操作

- `Add`：ID 非空、名称集合非空、字段合法；ID 已存在报 `ErrConflict`。
  校验全部通过且在写锁内复检通过后才落库。
- `Remove`：不存在报 `ErrNotFound`；若被删证书恰为默认证书，默认一并清空。
- `SetDefault` / `ClearDefault`：默认必须指向已存在证书。
- 操作即时生效；已返回的 `Selection` 是值拷贝，后续更新不影响它。

## 并发模型

`index` 结构受单个 `sync.RWMutex` 保护：

- 写操作（Add/Remove/SetDefault/ClearDefault）持写锁，多步索引修改对读者
  原子可见；读者不可能看到只更新了一半的精确表 / 通配表 / 默认指针。
- `Select` 先在锁外做不可变参数校验，再持**一次**读锁完成"找候选 → 判定
  有效性 → 排序"的全过程，因此该次选择对应某个确定的串行状态，整体执行
  等价于某种串行交错（linearizable）。

### 被放弃的方案

- **copy-on-write 不可变快照 + `atomic.Pointer`**：读路径只需一次原子加载，
  理论上最优雅；但每次变更都要 clone 全部 map（O(证书总数)），2 万次插入
  退化为 O(n²)，实测单测耗时 60s 超时。改为持久化 trie 后虽避免了 trie
  重建，但证书/精确表 map 仍需全量复制，热更新频繁时代价不可接受，故放弃。
- **无锁 CAS 交换快照**：写冲突时若不显式重试会静默丢更新（随机对照测试
  曾抓到两实现状态分叉）；加重试循环又与"变更即时生效"下的写放大冲突，
  放弃。
- 最终采用 RWMutex：读多写少场景下读锁互不阻塞，写锁仅在热更新瞬间短暂
  串行，简单且可证明正确。

## 选择复杂度（不随证书总数线性增长）

索引包含：

- `exact map[规范化名字] -> []证书ID`：精确命中一次哈希查找 O(1)。
- `wild map[通配基数] -> []证书ID` 加一棵**反向标签 trie**：
  对 `a.b.example.com`，从右向左沿 `com → example → b` 走
  `len(labels)-1` 步；终点为终端节点则存在通配 `*.b.example.com`。
  遍历长度只与**名字标签数**有关（域名 ≤ 127 个标签，实际是很小的常数），
  与通配证书总数无关；命中后只取该基数对应的 ID 列表。

因此选择的检查证书数 = 匹配同一名字/同一通配基数的候选数，而不是集合
总量。该性质以两种可验证方式证明：

1. `ExaminedCount()` 暴露最近一次选择实际检查的候选数；
   `TestExaminedCountBounded` 在 2 万、3 万证书下断言其恒为 4；
   `TestExaminedCountManyWildcardBases` 在 5002 张不同基数的通配证书中
   断言只检查 2 张。
2. `BenchmarkSelect20k`（约 0.4 µs/op）对比朴素全表扫描
   `BenchmarkSelectNaive20k`（约 3.4 ms/op），数量级差距直接可见。

## 本地验证

```bash
# 全量测试（含 -race 竞态检测）
GOCACHE=/tmp/gocache go test -race -count=1 ./...

# 仅随机对照（每次使用时间种子，多跑几轮）
GOCACHE=/tmp/gocache go test -count=30 -run TestRandomDifferential ./certselector/

# 导出每步输入/输出/判定依据的完整日志
CERTSELECTOR_LOG=/tmp/diff.log GOCACHE=/tmp/gocache \
  go test -run TestRandomDifferential -v ./certselector/

# 复杂度基准对比
GOCACHE=/tmp/gocache go test -run xxx \
  -bench 'BenchmarkSelect' -benchmem ./certselector/

go vet ./... && gofmt -l .
```

## 测试组成

- 定向用例：有效区间边界、通配零/多标签拒绝、非法 SAN/主机名、精确过期压过
  可用通配、默认回退分界、移除默认证书、并列偏好（EC>RSA>NotAfter>ID）、
  过期与密钥不支持并存时的拒绝次序、大小写与尾点规范化、被拒绝操作不改状态。
- `TestRandomDifferential`：1200 步随机 Add/Remove/SetDefault/ClearDefault/
  Select 序列，与独立的朴素模型（每次全量扫描、独立实现判定逻辑）逐步对照
  返回 ID、来源标记与错误分类；通过 `CERTSELECTOR_LOG` 输出含每步输入、输出
  与判定依据的日志（种子打印在测试输出中，便于复现）。
- 并发用例在 `-race` 下并行混合读写，验证不出现半更新与数据竞争。
