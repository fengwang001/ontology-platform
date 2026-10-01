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

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## NOR Flash 器件模型

NOR Flash 模型位于 `norflash` 包，构造函数为：

```go
device, err := norflash.New(S, Z, P, NOP, E)
```

- `S` 为扇区数，`Z` 为每扇区字节数，`P` 为每页字节数且必须整除 `Z`。
- `NOP` 是每页在两次擦除之间允许的最大成功编程次数；`E` 是每扇区允许的最大成功擦除次数。
- 初始容量为 `S*Z`，所有字节为 `0xFF`，所有页编程计数和扇区擦除计数均为 0。

### 编程与擦除语义

- `Program(addr, data)` 逐字节执行 `new = old AND data`，只能把位从 `1` 写成 `0`。
- 如果输入位为 `1` 而当前位为 `0`，该操作试图把 `0` 写回 `1`，属于非法位；整次编程拒绝，不修改任何字节或计数。
- 一次编程触及的每一页都计数一次；跨页、跨扇区时每个触及页各计一次。即使输入与现有内容完全相同也计数。
- `Erase(sec)` 成功时把扇区恢复为全 `0xFF`，清零该扇区全部页编程计数，并把扇区擦除计数加一。
- 第 `1..E` 次擦除成功，第 `E+1` 次返回寿命错误并保持状态不变。

### 被断电打断的擦除

`ErasePartial(sec, k)` 模拟擦除到 `k` 字节时断电：

- 扇区内偏移 `0..k-1` 的字节恢复为 `0xFF`，其他字节保持不变。
- 只有整页都落在前 `k` 字节内的页才清零编程计数；页边界落在 `k` 中间时，该页不清零。
- 擦除计数仍加一，并受相同的 `E` 次寿命限制。
- `k == 0` 时只增加擦除计数；`k == Z` 时与 `Erase(sec)` 的效果完全相同。

### 拒绝原因与顺序

所有被拒绝的操作都具有原子性，不会改变存储内容、页计数或扇区计数。

- 构造：任一参数小于 1，或 `P` 不整除 `Z` 时返回非法配置错误。
- `Program`：空 `data`、地址越界、触及页中最小页号计数已达 `NOP`、最小地址存在非法位。
- `Erase` / `ErasePartial`：先检查扇区越界，`ErasePartial` 再检查 `k` 是否在 `[0,Z]`，最后检查擦除寿命。
- `Read`：先检查负长度，再检查地址范围；`Read(capacity, 0)` 合法，并始终返回副本。
- 错误哨兵支持 `errors.Is`；编程页号和非法位地址通过 `ProgramLimitError` 与 `IllegalBitError` 精确返回。

所有方法由内部读写锁保护，可并发调用，结果等价于某一个串行执行顺序。查询方法包括 `PageProgramCount`、`SectorEraseCount`、`Capacity`、`PageCount` 等。

### NOR Flash 本地验证

```bash
# 常规测试，包含朴素逐字节模型对照
go test -v ./norflash

# 并发验证
go test -race -v ./norflash

# 全量检查
go test ./...
go vet ./...
gofmt -w norflash
```

如果系统没有全局 Go 缓存写权限，可指定临时缓存：

```bash
GOCACHE=/tmp/ontology-go-cache go test -race -v ./norflash
```

测试日志会打印每次操作的输入、输出或错误，以及 NOP、非法位、部分擦除页边界、寿命和重放一致性的判定依据。
