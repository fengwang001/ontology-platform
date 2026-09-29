# migration：键控状态的惰性模式迁移

`migration` 包在不触碰任何键的前提下升级模式版本；旧版本数据只在**被读取时**
沿迁移链逐版本迁移，全部步骤成功后**原子写回**。读取结果与“从原始版本
重跑整条链”一致且可复现。

## 版本与迁移链

- 版本号 `Version` 为从 1 开始的正整数；存储的初始版本由 `NewStore` 指定。
- 迁移函数按**来源版本**登记：`Register(v, fn)` 登记 `v -> v+1` 一步；
  同一来源版本只能登记一次（重复登记返回 `ErrMigrationExists`）。
- `Upgrade(next)` 只把当前版本推进到 `next`（必须严格更大），**不读写任何键**。
- 每个键的数据带有自己的存储版本；不同键可以停留在不同版本上。

## 读取、写回与去重规则

对键 `k` 调用 `Read`：

1. 存储版本 == 当前版本：直接返回数据拷贝（快速路径，不执行任何迁移）。
2. 存储版本 < 当前版本：
   - 先检查 `存储版本 -> 当前版本` 的**整条链**是否完整；只要缺一步，立即返回
     `ErrMissingMigration`，并且**一个迁移函数都不会被调用**；
   - 链完整时，只运行**存储版本之后**的步骤（例如键在 v3、当前 v5，只跑
     `v3->v4`、`v4->v5`，绝不重跑更早的步骤）；
   - 依次执行，任一步返回 error（或 panic）则整体失败，**存储保持原样**，
     下次读取会从头重试；
   - 全部成功后，在数据锁内以一次 map 赋值把 `(版本, 数据)` **原子写回**。
3. 写回成功后，该键再被读取即走快速路径，**已执行的步骤不会重复执行**。
4. 同一键的并发读取共用**一次**迁移执行（single-flight）：一个读者成为
   执行者持该键的门控锁同步跑链，其余读者等待同一个 `flight` 并取回逐字节
   一致的结果；不同键互不阻塞。
5. `Write` 始终以**当前版本**存数据拷贝；若同键迁移正在进行则等待其结束，
   绝不并发于迁移写回。
6. `Write` 与 `Read` 返回/存储的都是拷贝，调用方修改缓冲区不会污染存储。

`StoredVersion` 与 `Exists` 是只读存储查询，**不会**触发迁移；`SelfCheck`
  检查“现有数据的最低存储版本 → 当前版本”所需的整条链是否完整（空存储视为
  完整）。

## 边界与错误类别

所有错误都是可通过 `errors.Is` 区分的哨兵，互不相同：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidVersion` | 版本非正；`Upgrade` 目标不大于当前；存储版本异常超前 |
| `ErrInvalidKey` | 键为空串 |
| `ErrInvalidArgument` | 其它参数非法，如迁移函数为 `nil` |
| `ErrKeyNotFound` | 读取/查询的键不存在 |
| `ErrMissingMigration` | 迁移链不完整，某来源版本没有登记函数（零函数调用） |
| `ErrMigrationFailed` | 链上某一步返回错误或 panic，错误被 `%w` 包装，存储不变 |
| `ErrMigrationExists` | 同一来源版本重复登记 |

任何一次被拒的调用（非法参数、键不存在、缺迁移、迁移失败等）都不会改变
存储内容、当前版本或已登记的迁移表——失败不留痕。

其它边界：

- 迁移函数 panic 会被捕获并折算为 `ErrMigrationFailed`，flight 也会被清理，
  不会留下挂死的键。
- `context.Context` 被取消时进行中的读取中止，同样不写回。
- 门控锁只串行化**同一键**的迁移与写回，不影响其它键的并发。

## 日志

传入 `WithLogger(Logger)` 可接收每一步的输入、返回与判定依据，例如：

```
migration: read key="k" stored=v1 current=v4: decision=run_chain steps=3
migration: step key="k" v1->v2: input_bytes=1
migration: step key="k" v1->v2: return_bytes=2: decision=ok
migration: writeback key="k" stored=v1->v4 bytes=4: decision=committed
migration: read key="k" stored=v4 current=v4: decision=fast_path
```

缺迁移时打印 `reason=missing_step ... calls=0`；失败时打印
`return_error=... decision=keep_storage_unchanged`。默认使用标准库 `log`。

## 最小用法

```go
store, _ := migration.NewStore(1)
store.Register(1, func(_ context.Context, from migration.Version, d []byte) ([]byte, error) {
    return append(d, '2'), nil // v1 -> v2
})
_ = store.Write(ctx, "user:1", []byte("v1data"))
_ = store.Upgrade(2)           // 不触碰任何键
out, err := store.Read(ctx, "user:1") // 惰性迁移并写回
```

## 本地验证

```bash
# 格式化与静态检查
gofmt -l .
go vet ./...

# 全量测试（含竞态检测、重复执行）
go test -race -count=3 ./...

# 详细查看单个用例
go test -race -v -run TestConcurrentSameKeyMigrationRunsOnce ./migration
```
