# 分层配置版本化发布与解析系统 — 设计说明

实现位于 `layercfg/` 包，无第三方依赖。核心模块按职责拆分：

| 文件 | 职责 |
| --- | --- |
| `errors.go` | 错误类别（`ErrorKind`）与统一的优先级编号 |
| `types.go` | 层级、层引用、解析三元组、值类型、合并方式、变更等全部领域类型 |
| `schema.go` | 键模式登记的自身合法性校验（类型/合并方式/整数范围） |
| `value.go` | 类型化值的构造、类型与范围校验、不可变复制、值比较 |
| `snapshot.go` | 不可变版本快照、层引用/三元组合法性、键状态的写时复制 |
| `resolver.go` | 从宽到窄的层链、覆盖/追加/取消的合并语义、已存在实体枚举 |
| `publish.go` | 分阶段发布校验、同发布冲突、锁定冲突、必填传播、候选快照构造 |
| `store.go` | 并发控制、模式登记、原子发布、回滚、按历史版本读取 |

## 模型

四层从宽到窄：全局 `global` → 环境 `env(env)` → 区域 `region(env,region)` →
实例 `instance(env,region,instance)`。解析三元组 `Scope{Env,Region,Instance}` 中
后两者可缺省，层链 `layerRefs` 只包含被逐级带齐的层（1~4 个）。

每个已登记键在每个层引用上最多有两个相互独立的**槽位**：

- 值槽：无 / 普通值 / 取消标记（`WriteValue | WriteCancel`）。
- 锁槽：是否锁定（布尔）。

“清除该层该键的写入”（`OpClearWrite`）只作用于值槽；“锁定/解除锁定”
（`OpLock/OpUnlock`）只作用于锁槽。二者互不影响，因此同一次发布中可以对
同一层同一键既解除/设置锁定又写入值。

## 关键取舍

### 1. 不可变快照 + 每键写时复制，而不是每次发布整盘复制

每次发布成功只新建一个 `snapshot` 根（一个 `map[string]*keyState`），
把父版本的键状态指针整体共享；仅对本次发布涉及的键执行一次
`cloneForWrite`（复制该键的两个小 map）。因此：

- 历史版本的存储增量 = O(本次发布涉及的不同键数)，与配置总键数无关。
- `TestStructuralSharing` 用指针相等直接验证：未变键父子版本共享同一
  `*keyState`，被改键必然是不同指针。
- `BenchmarkPublishSharesHistory` 中键总数从 100 增到 4000（40 倍），
  单键发布的字节分配保持同量级（约 1.1KB→1.6KB，主要是 map 桶的常数差异），
  证明没有复制整份配置。

回滚不“改写历史”：它创建一个新版本，新版本的 `keys` 直接指向目标历史
版本的 `keys` map（内容完全相同），目标版本与此前所有版本仍留在
`history` 中且永不删除。

### 2. 解析复杂度 O(1) 于总键数/层数/版本数

解析单键：一次 map 查键 + 遍历固定长度 4 的层链，追加型列表再遍历该链上
的列表元素。它不扫描其他键、不扫描其他层、不回放历史。

`BenchmarkResolveScaling` 中（50 键/50 版本）与（2000 键/2000 版本）的
解析耗时分别约 256ns 与 311ns、分配均为 2 次 336B，差异只是 Go map 的
常数项，随规模增长不上升，可复现地证明解析开销不随总键数、层总数、
历史版本总数增长。

### 3. 模式（schema）不受版本影响

模式表单独存于 `Store.schemas`，所有版本共用，读取历史版本也用最新模式
解释类型与合并方式。回滚到历史版本时，用“当前最新模式”重做必填校验：
目标版本在它产生时必然满足当时模式，但若其间模式被修改（例如把某键改成
必填），回滚可能因 `ErrRequiredMissing` 失败——这是规格中明确要求的唯一
例外。

### 4. 校验分阶段、严格按错误优先级短路

错误优先级（数值越小越高）在 `errors.go` 固定：

1. `ErrInvalidArgument` 参数非法（层限定不匹配、操作种类未知、SetValue
   未带类型化值、非写值操作携带值、解析三元组缺父限定、回滚负版本号）。
2. `ErrVersionNotFound` 版本不存在。
3. `ErrKeyNotRegistered` 键未登记。
4. `ErrTypeOrRange` 值类型不符或整数越界。
5. `ErrLockConflict` 锁定冲突。
6. `ErrConflict` 同发布内对同一层同一键同一槽位的多条变更。
7. `ErrRequiredMissing` 必填缺失。

`validatePublish` 严格按上述顺序逐阶段短路；任何阶段失败都只丢弃候选
快照，发布返回当前版本号且不动状态。注意“同发布内冲突”在构造候选
快照**之前**判定（第 4 阶段前置），保证它优先于锁定冲突。

### 5. 锁定冲突在“候选快照”上统一判定

锁定语义：某层锁定某键后，锁定所在链的**更窄层**既不能写值也不能取消；
不约束本层，也不约束更宽层。链的归属由限定名决定，`narrowerInChain`
要求层级严格更窄且在锁定层级及以上的限定名完全相同：

- 全局锁约束所有 env/region/instance；
- `env=prod` 锁约束 prod 下全部区域/实例，不约束 env=dev；
- `region=prod/cn` 锁只约束 prod/cn 下实例，不约束 prod/us。

通过先构造候选快照、再扫描“锁 × 写入”，同一套判定同时覆盖两种情形：
发布前更窄层已经存在的写入（新增锁），以及同次发布在更窄层的新写入。
因此“先清理窄层写入再锁定”必须同次带上 `OpClearWrite`，否则整次发布
被拒（见 `TestLockPreexistingNarrowWrite`）。

### 6. 必填传播与“实体存在性”

实体（环境/区域/实例）是否存在，以**任一层的值槽写入**（值或取消标记都
算写入；锁定不算）中出现过其限定名为准。`entityScopes` 枚举：

- 全局视图 `Scope{}`：检查全局必填；
- 每个出现过的 env；
- 每个出现过的 (env,region)；
- 每个出现过的 (env,region,instance)。

对每个实体，按其解析视图（从全局到该实体的叠加结果）检查全部必填键是否
`Present`。由于窄视图天然继承宽层值，在全局写一个必填值即可让所有实体
满足；但任何一层的取消标记会作废该链上更宽的累积，可能使该实体及其窄链
缺失，从而拒绝发布。

### 7. 未设置与空值

解析结果 `Result.Present` 显式区分：

- `Present=false`：链上没有任何有效值（被取消清空后也没有再写入）。
- `Present=true` 但值为空串 `""` 或空列表 `[]string{}`：仍是已设置值。

追加列表在取消之后首次重新写入会得到全新的空列表再追加；空列表写入也是
一个 `Present=true` 的值。

### 8. 并发：单把 RWMutex 保证可串行化、读不观察半成品

`Publish`/`Rollback`/`RegisterKey` 取写锁，`Resolve`/`ResolveAll`/
`Current`/`Schema` 取读锁。候选快照在校验期间是发布 goroutine 的局部
对象，只有全部校验通过后才在写锁内把 `current` 指针一次性切换并追加
历史；解析始终读取一个已发布的不可变快照指针，因此不可能观察到发布进行
到一半的状态。任意并发交错的可观察结果都等价于某个获得写锁的串行顺序。

## 被放弃的方案

- **操作日志回放（event sourcing）重建版本**：回滚与历史读取需要回放从 0
  到目标版本的全部变更，读取成本随历史版本数增长，直接违背“解析不随
  版本总数增长”的要求，故放弃。
- **每次发布整盘深拷贝配置**：实现最简单，但历史存储 O(总键数 × 版本数)，
  被结构共享方案取代。
- **持久化到磁盘 / 后台压缩**：需求只要求内存内精确可复现与并发安全，
  引入持久化会增加与语义无关的复杂度；快照本身已是可序列化的纯数据，
  将来可在不改核心语义的前提下增加快照落盘。
- **在 Resolve 时惰性合并所有版本的增量**：同样会让读取依赖版本数，放弃。
- **更细粒度的每键锁**：可提高写并发，但会让“整次发布全有或全无 + 必填
  全局传播”的事务边界复杂得多；当前发布是批量短事务，单写锁的简单正确
  性更重要。

## 本地验证方法

需要 Go 1.26+（仓库 `go.mod` 为 1.26.5）。若 `go` 不在 PATH：

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/gocache   # 仅当默认缓存目录只读时需要
```

```bash
# 全量测试（含 -race 并发可串行化检测）
go test -race ./...

# 详细查看随机对照测试逐步打印的“输入 / 实际输出 / 判定依据”
go test -v -run TestRandomDifferential ./layercfg/
DIFF_SEED=12345 go test -run TestRandomDifferential ./layercfg/   # 换随机种子

# 场景测试
go test -v -run 'TestFourLayer|TestCancel|TestAppend|TestUnset|TestLock|TestRequired|TestRollback|TestError|TestIntRange' ./layercfg/

# 性能与结构共享证据
go test -run 'TestStructuralSharing|TestConcurrent' -v ./layercfg/
go test -bench . -benchmem ./layercfg/

go vet ./...
gofmt -l .
```

## 测试覆盖对照

- 四层叠加、缺省三元组：`TestFourLayerOverride`
- 取消对覆盖型的作废与窄层再写入：`TestCancelReplaceThenNarrowRewrite`
- 追加去重保留首次位置：`TestAppendDedupFirstPosition`
- 取消清空追加累积后窄层重写：`TestCancelAppendClears`
- 未设置 vs 空串/空列表：`TestUnsetVsEmpty`
- 锁定只约束窄层、本层/宽层可写、解锁/重锁：`TestLockConstraints`
- 发布前已存在窄层写入必须先清理：`TestLockPreexistingNarrowWrite`
- 必填在全局/环境/区域/实例的传播与取消反例：`TestRequiredPropagation` 等
- 回滚到历史版本、回滚当前版本无操作、目标不存在报错、历史读取：
  `TestRollbackAndHistory`
- 错误优先级与失败发布的原子性：`TestErrorPriorityAndAtomicity`
- 大量随机发布/回滚/历史解析与独立朴素模型逐步对照（含日志）：
  `TestRandomDifferential`（3 个种子 × 4000 步）
- 结构共享：`TestStructuralSharing`
- 并发可串行化、无撕裂读、版本连续：`TestConcurrentSerializability`
