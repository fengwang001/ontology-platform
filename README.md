# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 共享页缓存 cgroup 记账（`pagecache` 包）

`pagecache.Cache` 模拟一个被多个 cgroup 共享的页缓存：物理页容量始终只
记在一个“所有者” cgroup 名下，引用增减时所有权在 cgroup 间原子迁移。

### 引用、持有者与所有者

- 一个映射（mapping）是带名字的页序列，同一页 ID 在序列中每出现一次计
  一次引用。`ref(c, id)` 是 cgroup `c` 全部映射对页 `id` 的引用次数之和。
- `ref(c, id) > 0` 的 cgroup 称为该页的**持有者**。
- `since(c, id)` 是 `ref(c, id)` 最近一次由 0 变为正的那笔 Map 的 `now`；
  只要引用保持为正它就不变（同 cgroup 重复 Map 同一页不会刷新），降到 0
  后再次升起时重新取当时的 `now`。
- 页的**所有者**是持有者中按 `(since 升序, cgroup 名字节序升序)` 最小者；
  页没有持有者时即被回收，缓存同时忘掉它的大小，之后同 ID 可以换大小
  重新存入。
- `Used(c)`（物理用量）= `c` 拥有的全部存活页大小之和；`Logical(c)`
  （逻辑用量）= `c` 持有至少一次引用的不同页大小之和；`Over(c)` 当
  `Used(c)` 严格大于限额时成立；`Total()` 为全部存活页大小之和。

### Map 的 delta、限额与容量判定

对本次涉及的每个不同页，先算出操作**之后**的所有者：

- 若操作之后归本 cgroup 所有、而操作之前不是，则该页大小计入 `delta`
  （既包括缓存里全新的页，也包括 `since` 取等时因名字更小而“夺权”）。
- `fresh` 是缓存中尚不存在的不同页大小之和。
- 判定顺序：先全局容量 `Total + fresh > cap`（恰等通过）报 `ErrCapacity`，
  再本 cgroup 限额 `Used(c) + delta > limit(c)`（恰等通过）报 `ErrLimit`。
- `delta == 0` 的 Map（纯引用已有页且未夺权）即使该 cgroup 已超额也放行，
  此时 `fresh` 必为 0，故也不占用容量。

### Unmap 与所有权迁移

Unmap 永不因限额或容量被拒。映射的引用逐页扣除：某 cgroup 对某页的
`ref` 降为 0 时它退出持有者；若它原是所有者且仍有其他持有者，所有权迁给
剩余持有者中 `(since, 名字)` 最小者，其 `Used` 增加该页大小（允许因此
进入超额）；无任何持有者时页被回收，`Total` 相应减少。

### 时钟、错误次序与并发

`Map`/`Unmap` 携带单调不减（允许相等）的非负时钟；被拒绝的操作不会推进
时钟，也不改变任何状态（含 `since`、页缓存与引用）。错误次序：

- Map：`ErrParam` → `ErrClock` → `ErrNoLimit` → `ErrExists` →
  `ErrSizeMismatch` → `ErrCapacity` → `ErrLimit`。
- Unmap：`ErrParam` → `ErrClock` → `ErrNoLimit` → `ErrNotFound`。
- SetLimit：仅 `ErrParam`。

全部方法可用一把读写锁并发调用，Map/Unmap 对外部是原子步骤。不变量：
所有 cgroup 的 `Used` 之和恒等于 `Total`；每个存活页恰有一个所有者且其
必为持有者；持有者 ref 之和恒等于全部映射内页出现次数之和。`Used` 与
`Total` 均为 O(1) 读取增量维护的计数器（`rescans` 恒为 0）。

### 本地验证

```bash
# 全量测试（含 2000 组随机序列对朴素模型的差分对照与不变量校验）
go test ./pagecache/

# 竞态检测下运行（推荐）
go test -race ./...

# 查看随机对照日志：输入、双方输出与判定依据
go test ./pagecache/ -run TestRandomDifferential -v

# 跳过耗时的差分模糊
go test -short ./...
```

定向用例覆盖 `since` 取等按名字决胜、同 cgroup 重复引用 `since` 不变与
归零后重取、所有者离开时按 `since`（而非字节序）迁移、迁移致接收者超额
后带新页 Map 被拒而纯去重 Map 放行、delta/容量取等与大 1 拒绝、容量先于
限额、夺权计入 delta 且拒绝不改 `since`、映射内重复 ID 的计数与单次
fresh/delta、回收后同 ID 换大小、以及完整错误次序。

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
