# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## inlinecache：属性访问内联缓存站点管理器

`inlinecache` 包按站点维护内联缓存，全局方法表把形状编号（正整数）映射到目标。

### 状态迁移

- 站点缓存最多 M 个（形状，目标）条目，状态由条目数决定：
  - 0 个：`empty`（空）
  - 1 个：`monomorphic`（单态）
  - 2..M 个：`polymorphic`（多态）
  - 溢出：`megamorphic`（超多态），一旦进入永不退出
- 未命中且缓存已有 M 个条目时：清空缓存并进入超多态，该次访问仍计一次未命中。
- 重定义导致的条目删除会使状态随条目数回落（多态 → 单态 → 空），但超多态不受影响。

### 统计口径

- 命中（Hits）：非超多态站点命中缓存条目。
- 未命中（Misses）：非超多态站点未命中（含触发进入超多态的那次）。
- 超多态访问（Megamorphic）：超多态站点的访问，直接查全局方法表，不计命中/未命中。
- 不变式：每个站点 `Hits + Misses + Megamorphic` 恒等于该站点成功访问的总次数。
- 被拒绝的操作（见下）不改变任何站点、统计与方法表。

### 失效规则

- 新定义形状：不影响任何站点。
- 重定义且目标不同：所有缓存了该形状的非超多态站点删去该条目；超多态站点不受影响。
- 重定义且目标相同：无任何影响。

### 拒绝场景（原因可区分）

- `NewManager` 的 M < 2：`ErrInvalidCapacity`
- 定义/访问的形状不是正整数：`ErrInvalidShape`
- 创建已存在的站点名：`ErrSiteExists`
- 访问/查询不存在的站点：`ErrSiteNotFound`
- 访问未在方法表中定义的形状：`ErrShapeUndefined`

### 并发与确定性

所有公开方法由互斥锁串行化，可并发调用，结果等价于某个串行顺序；
相同操作序列重放得到完全相同的状态与统计。

### 本地验证

```bash
# 全部测试（含朴素模拟对照、确定性重放、并发不变式）
go test ./inlinecache/

# 竞态检测 + 打印输入/输出/判定依据日志
go test -race -v ./inlinecache/
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
