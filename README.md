# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## hashdir：带改名与上一代游标迁移的哈希序目录读游标

`hashdir` 包（`hashdir/`）把目录项按 `(h(name), minor)` 排成稳定顺序，并用游标分页读取。

### 哈希与 minor

- 哈希：初值 `h = 2166136261 XOR seed`，对名字的每个字节依次 `h = (h XOR b) * 16777619`，全程模 2^32（FNV-1a 变体）。
- 目录项键为 `(h, minor)`：`minor` 是同一 `h` 的当前存活项中未被占用的最小非负整数；排序键 `key = h*2^32 + minor`（uint64），按 key 升序。
- `Add(name, ino)` 插入并返回 key；`Remove(name)` 删除后其 minor 立即可被后来者复用；`Rename(old, new)` 保持 ino，先删 old（minor 立即空出）再按新名字的哈希取最小空闲 minor 插入（同哈希时可复用刚空出的 minor）。
- 构造参数 `M` 为同哈希存活项上限（1 到 2^31−1，含两端），超限报 `ErrHashFull`。

### 游标与 Done

- 游标 `Cookie{Gen, Pos}`：`Pos == 0` 表示起点且不检查代数；否则 `Gen` 须等于当前代数，或等于当前代数减一（可迁移，见下）。
- `ReadDir(c, n)` 返回 key 不小于 `c.Pos` 的前 n 项（升序），新游标为 `(当前代数, 最后返回项 key+1)`；一项未返回时原样返回 `c`（迁移成功则返回迁移后的游标）。
- `Done` 当且仅当不存在 key 不小于新游标 `Pos` 的项。`n == 0` 合法，返回空列表并仍按定义计算 `Done`；`n < 0` 报 `ErrInvalidArgument`。
- `CookieOf(name)` 返回 `(当前代数, 该项 key+1)`，用于从它之后续读。

### 重哈希与游标迁移

- `Rehash(newSeed)` 使代数加 1（即使种子不变）：先把重哈希前的「名字 → key」映射整份保存为上一代快照（只保留一份，覆盖更早的），再把全部项按名字字节序升序重新插入；任一 `h` 下项数超过 `M` 则整体拒绝，代数与快照都不变。
- 迁移：`Gen == 当前代数 − 1` 且 `Pos != 0` 时，若上一代快照中存在 key 等于 `Pos−1` 的名字 q 且 q 此刻仍存在，则游标按 `(当前代数, q 的当前 key+1)` 续读；否则报 `ErrStaleCookie`。快照在 Rehash 时固定，之后的增删改名不影响快照。
- 所有拒绝原因可用 `errors.Is` 区分：`ErrInvalidArgument`、`ErrAlreadyExists`、`ErrHashFull`、`ErrNotFound`、`ErrStaleCookie`；被拒绝的操作不改变任何状态。
- 全部方法可并发调用，结果等价于某个串行顺序；相同操作序列重放得到逐项相同的 key 与代数。

### 本地验证

```bash
go test ./hashdir/            # 全部定向测试 + 2000 组随机序列对照朴素模型
go test -race ./hashdir/      # 竞态检测
go test ./hashdir/ -run TestRandomAgainstModel -v   # 查看每步输入/输出/判定依据日志
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
