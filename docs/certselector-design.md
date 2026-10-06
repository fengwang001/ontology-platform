# 加密传输终结网关证书选择器设计

## 范围

`certselector` 根据 SNI/主机名、客户端支持的密钥类型和当前时间，从可热更新的证书集合中选择证书。选择结果区分精确匹配、通配匹配和默认回退；失败区分无匹配、全部不在有效区间、有效证书的密钥类型均不被支持。

## 公共模型

- `Certificate`：`ID`、规范化后的 SAN 列表、`KeyType`、`NotBefore`、`NotAfter`。
- `KeyTypeEC` / `KeyTypeRSA`：仅支持椭圆曲线和 RSA。
- `SelectInput`：`Name`、非空的 `KeyTypes`、非负整数 `Now`。
- `Selection`：返回证书副本及 `MatchExact`、`MatchWildcard` 或 `MatchDefault`。
- 哨兵错误：`ErrInvalidArgument`、`ErrCertificateConflict`、`ErrCertificateNotFound`、`ErrNoMatchingCertificate`、`ErrNoValidCertificate`、`ErrUnsupportedKeyType`。

## 名称规则

客户端名和 SAN 在比较前转为小写，并删除一个末尾点。规范化后为空、包含空标签或空白、单标签超过 63 字节、总长超过 253 字节均拒绝。

通配 SAN 必须是最左完整标签为 `*` 的形式，例如 `*.example.com`。`a.*.com`、`*a.example.com`、`*.com`、`*.` 均非法。`*.example.com` 只匹配 `api.example.com` 这类恰好多一个标签的名称，不匹配 `example.com` 或 `a.api.example.com`。

证书 SAN 按集合处理：重复项规范化后去重；集合为空拒绝。`NotAfter < NotBefore` 拒绝；`NotAfter == NotBefore` 表示左闭右开的空有效区间，可添加但任何时刻都不可用。

## 选择规则

1. 先验证所有参数；参数非法优先于任何选择失败。
2. 非空名称先找精确名桶；只要精确桶存在，即使其中证书全部不可用，也不再看通配桶或默认证书。
3. 没有精确桶时才找该名称父域对应的通配桶。
4. 名称为空或两种名称桶都不存在时才使用默认证书。
5. 同名候选先比较密钥类型：椭圆曲线优先于 RSA；再比较 `NotAfter`，更晚优先；最后按 `ID` 字典序，较小优先。
6. “有有效证书但类型不支持”优先于“全部不在有效区间”。

## 数据结构与复杂度

选择器维护：

- `map[id]*certificateRecord`：证书主表。
- `map[normalizedName]*scheduleGroup`：精确 SAN 桶。
- `map[wildcardParent]*scheduleGroup`：通配 SAN 桶，桶键是 `*.` 后的完整父域。
- `defaultID`：默认证书标识。

每个 `scheduleGroup` 收集同一个名称下的证书，并以该桶内所有 `NotBefore/NotAfter` 建立时间边界。构建时按时间扫描开始和结束事件，用两个优先堆分别维护当前有效的 EC 与 RSA 证书，预生成每个时间区间的最优 EC/RSA 指针。查询只做 map 定位和边界二分。

- 选择：平均 `O(log k)`，其中 `k` 是同一个精确名或同一个通配父域下的证书数；与集合总证书数 `n` 无关。
- 添加/移除：更新证书涉及的名称桶；桶构建为 `O(k log k)`。
- 设置/清除默认证书：平均 `O(1)`。
- 空间：证书的每个 SAN 在一个桶中记录一次；桶时间索引为桶内证书数的线性规模。

`BenchmarkSelectSmallPopulation` 与 `BenchmarkSelectLargePopulation` 使用相同同名桶规模、分别加入 100 和 100000 张无关证书，用来观察总集合增长不使选择耗时线性增长。`BenchmarkSelectLargeMatchedGroup` 单独观察同名桶增长对时间二分的影响。

## 并发与热更新

`sync.RWMutex` 串行化所有写操作，并允许选择并发持有读锁。所有参数校验发生在修改前；冲突、不存在或非法操作直接返回，不会修改主表、名称桶或默认证书。写操作在同一把锁内完成所有桶替换，因此选择不可能读到只更新了一半的逻辑集合。读锁使整次选择对应某个已经完整提交的状态，选择结果等价于一次串行历史。

选择成功时返回内部证书的副本，调用方之后修改返回值的 `Names` 切片不会影响选择器。

## 放弃的方案

- **每次选择扫描全部证书**：实现最简单，但选择复杂度为 `O(n)`，明确不满足规模要求。
- **全局时间索引**：虽然也能按时间查询，但仍需再处理名称匹配和精确/通配优先级，且热点名称会与无关证书共享全局结构；按名称分桶更直接。
- **在查询时遍历同名桶并过滤时间/类型**：选择复杂度随同名桶增长，只适合作为测试中的朴素模型，不适合生产实现。
- **每次更新复制整个选择器状态做 RCU**：读侧简单，但一次添加/移除会复制全部名称索引，写成本随总集合增长；当前方案只重建受影响名称桶。

## 验证方法

在仓库根目录运行：

```bash
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache go test ./...
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache go test -race ./...
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache go test ./certselector -run '^$' -bench BenchmarkSelect -benchtime=1000x
```

随机对照测试使用固定种子，执行 1200 组、每组 18 步随机操作。测试内的朴素模型独立保存状态并暴力扫描，不使用生产索引；逐步比对错误、证书 ID 与来源。使用：

```bash
PATH=/usr/local/go/bin:$PATH GOCACHE=/tmp/go-cache go test -v ./certselector -run TestRandomizedModelComparison
```

可打印每一步输入、输出和判定依据。
