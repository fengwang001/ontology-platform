# 设计说明

## 结构
- `schema`：不可变值对象。`Version` 为有序字段列表，构造时校验字段数/名称/类型/重名，并附带 `name -> 字段` 映射，保证按名取字段 O(1)，不线性扫描。`Project(version, fields)` 按版本原次序投影，给订阅者视图使用。
- `compat`：纯函数 `CanRead(reader, writer)`，按读者字段次序返回第一个违规（`MissingNoDefault`/`TypeMismatch`/`OptionalToRequired`）；提升只认 int32→int64、int32→float64、string→bytes 与同型。包内未导出计数器 `compared` 累加检查过的 R 侧字段数，经未导出访问器在测试前重置。
- `gate`：`Registry` 持有全部主题，单个 `sync.RWMutex` 保护；每次操作在临界区内完成「校验→判定→提交」，拒绝不改任何状态，因此并发等价于某个串行顺序，状态确定可重放。

## 关键取舍
- 订阅者检查只对其 `fields` 投影后的读者视图做，不查整份 pinned 版本：未使用的字段不应阻塞发布；代价是 `fields` 必须在 pinned 中存在。
- 已 `Lagging` 的订阅者后续既不检查也不需豁免：它已确认读不了，永久阻塞没有意义；`Advance` 显式恢复并清豁免。
- 发布判定严格按 非法参数 > 时钟回退 > 主题不存在 > 重复/订阅者不存在 > 版本/字段不存在 > NoChange > Incompatible > Blocked/StillBroken 短路，只用哨兵错误包装，`errors.Is` 可区分。
- 豁免是墙钟区间 `[0, until)`：`now < until` 有效，相等即失效；重复登记覆盖。
- 模式检查方向固定：BACKWARD=新读者读旧数据 `CanRead(N,latest)`；FORWARD=旧读者读新数据 `CanRead(latest,N)`；FULL 两者，先 BACKWARD。订阅者检查与 mode 无关，永远是旧读者读新数据。

## 放弃的方案
- 每版本存全量字段副本做「整版本订阅者检查」：会被未使用字段错误阻塞，放弃，改用投影。
- 按名查找时线性遍历写方字段：违反复杂度要求，改为版本构造时建立 map。
- 细粒度每主题/每订阅者锁：复杂且无额外收益，单锁临界区内无外部调用（compat 为纯函数），足以保证可串行化。

## 本地验证
- `go build ./... && go vet ./...`
- `go test ./...`：表驱动覆盖提升/反向、三类违规次序、mode 结论相反、豁免边界、Lagging、Advance 原子性、拒绝次序；
  随机 1500 组操作序列与逐字段朴素模拟器对照（打印输入/输出/判定依据）；
  `compared` 上界按 10 与 1000 历史版本两档核对；`go test -race` 验证并发。
