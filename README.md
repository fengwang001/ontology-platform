# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## flush 包：缓冲池刷写链表与写依赖管理器

`flush` 包实现按首次弄脏 LSN 排序的脏页刷写链表、日志落盘水位与页间写依赖管理。
构造：`m, err := flush.NewManager(D)`，`D` 为脏页上限（1 到 10^6）。

### oldest 与 p.lsn 的区别

- `p.lsn` 是页的最新修改 LSN，每次 `Modify(p, lsn)` 都会更新。
- `oldest` 是本轮脏周期的首次弄脏 LSN：页从干净变脏时取当前修改 LSN；
  脏且非在途时的后续修改不改变它；在途期间被修改时记录 `firstAfter`，
  刷写完成（`FlushDone`）发现页在在途期间被改过时，`oldest` 改为 `firstAfter`
  并按升序插回链表相应位置（不是末尾）。恒有 `oldest <= p.lsn`。

### 在途期间修改的处理

`FlushStart(p)` 记录快照 `snap = p.lsn` 并置在途。`FlushDone(p)` 时：
若 `p.lsn == snap`（在途期间无修改），页变干净、离开链表并删除所有从 p 发出的依赖边；
否则页仍脏、不再在途，`oldest = firstAfter`，`firstAfter` 清空后按序插回链表。

### 前置依赖的登记与解除

- `AddDep(a, b)` 表示 a 必须先于 b 刷写。仅当 a 此刻为脏时才登记（a 干净时返回成功但不登记）；
  重复登记幂等；若会形成环（已存在 b 到 a 的路径）则拒绝且不登记。
- 依赖边只从脏页发出：页变干净（`FlushDone` 无在途修改）时其全部出边被删除；指向干净页的入边保留。
- `FlushStart(p)` 要求 `p.lsn` 不大于已落盘 LSN，且 p 的所有前置页此刻都不脏（在途也算脏）。

### 检查点与计划的推导

- `Checkpoint()` 返回链表首页（oldest 最小，含在途页）的 `oldest`；
  链表为空时返回已接受的最大 Modify LSN 加一（从未 Modify 时为 1）。
- `Plan(target)` 不改变任何状态：按链表次序遍历 `oldest < target` 且不在途的页，
  对每个未入计划的页递归地先按页号升序展开其脏且非在途的前置页（即使前置页
  `oldest >= target` 也被拉入），再把页本身追加进计划。

### 拒绝语义

所有拒绝原因以 `*flush.Error` 的 `Code` 区分（参数非法、LSN 不够大、脏页已满、
页不脏、页已在途、日志未落盘、前置页未刷、页不在途、成环），每个操作按规格顺序
只报第一个命中的原因；被拒绝的操作不改变任何状态，被拒的 `Modify` 不推进最大 LSN。
所有方法可并发调用，效果等价于某个串行顺序。

### 本地验证

```bash
# 单元测试 + 2000 组随机序列与朴素模拟对照（含竞态检测）
go test -race ./flush/

# 查看随机对照的输入、输出与判定依据日志
go test -race -run TestRandomAgainstNaive -v ./flush/
```

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
