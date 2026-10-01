# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## Maglev 式一致性哈希查找表（`maglev` 包）

`maglev.Table` 是一个带后端权重与限速迁移的 Maglev 式一致性哈希查找表，
位于 `maglev/` 目录。

### 构造

```go
tb, err := maglev.New(M, Lim)
```

- `M`：表大小，必须是 2 到 65537 之间的素数。
- `Lim`：每次迁移最多改动的可选槽数，必须在 1 到 `M` 之间。
- 配置不合法时整体拒绝（返回 error），不产生任何对象。

后端登记：`AddBackend(name, offset, skip, weight) (changed int, err error)`。

- `name` 为非空字符串；`offset ∈ [0, M−1]`；`skip ∈ [1, M−1]`；
  `weight ∈ [1, 16]`；后端总数不得超过 `M`。
- 拒绝原因按以下顺序只报第一个：name 为空（`ErrEmptyName`）→
  参数越界（`ErrInvalidBackend`）→ name 已存在（`ErrBackendExists`）
  → 后端数已达 M（`ErrTableFull`）。
- `RemoveBackend(name)` 对不存在的名字返回 `ErrBackendNotFound`。
- 被拒绝的操作不改变当前表与后端集合。

### 目标表 T* 的构造

每次登记或移除成功后，仅依据当前后端集合从零重新计算目标表 `T*`，
与登记/移除顺序无关：

1. 后端按名字字节序升序排列为 b0 … b(n−1)。
2. 后端 i 的排列为
   `perm_i(j) = (offset_i + j × skip_i) mod M`，`j = 0 … M−1`，
   并维护跨轮持久、不回零的指针 `j_i`（初值 0）。
3. 表初始全空，循环直到填满：按 b0 到 b(n−1) 的顺序，每个后端在自己的
   一轮内依次占据 `weight_i` 个槽；每次从 `perm_i(j_i)` 起向后扫描，
   越过所有已被占的槽（指针随之前进），占据遇到的第一个空槽并将指针
   再加一。
4. 表一旦填满立即停止：本轮剩余的占据与尚未轮到的后端都不再执行。

因为 M 为素数且 1 ≤ skip < M，排列是 0 … M−1 的一个完整置换，所以
只要表中尚有空槽，每个后端都能在 M 次探测内找到空槽。

### 迁移规则（cur → T*）

当前表 `cur` 不直接等于 `T*`，而是分批迁移。槽位分为两类：

- 必改槽：`cur[s]` 为空，或其所属后端已不在当前后端集合中。
- 可选槽：`cur[s]` 的后端仍在集合中，但 `cur[s] != T*[s]`。

迁移时机与数量：

- `AddBackend` / `RemoveBackend` 成功后：先把全部必改槽改成 T* 的归属
  （必改槽不受 Lim 限制且先于可选槽），再按槽位号升序最多改 `Lim` 个
  可选槽。
- `Step() (changed int)`：不改变后端集合，仅按槽位号升序最多改 `Lim`
  个可选槽；后端集合为空时什么都不做。
- 返回值都是本次实际改变归属的槽位数。
- `Pending() int`：返回 `cur` 与 `T*` 不同的槽位数。`Step` 恰好使
  Pending 减少 `min(Lim, Pending)`，反复 Step 至多 ⌈M/Lim⌉ 次后必为 0；
  Pending 为 0 时 `cur` 与 `T*` 逐槽相同。
- 从空表登记第一个后端、以及移除最后一个后端，都会改动全部 M 个槽；
  移除最后一个后端后 `cur` 全空。

### 查询

- `Lookup(h uint64) (string, error)`：返回 `cur[h mod M]` 的后端名；
  后端集合为空（cur 全空）时返回 `ErrNoBackends`。
- `Table() []string`：返回 `cur` 的拷贝。

所有方法通过互斥保证并发安全，结果等价于某个串行顺序；只要后端集合
非空，每次操作之后每个槽位都有归属且归属的后端都在集合中。相同的
操作序列重放得到完全相同的返回值与表。

### 本地验证

```bash
# 全量测试（含 2000 组随机后端集合/操作序列与朴素参考实现的逐步对照）
go test ./maglev/ -v

# 仅看带“输入 / 输出 / 判定依据”日志的小规模对照演示
go test ./maglev/ -v -run TestRandomDifferentialLogged

# 竞态检测
go test -race ./...

# 静态检查
go vet ./...
gofmt -l .
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
