# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## NOR Flash 器件模型（`norflash` 包）

`norflash` 实现了一个可精确复现的 NOR Flash 器件模型，构造参数为扇区数 `S`、
扇区字节数 `Z`、页字节数 `P`（须整除 `Z`）、每页两次擦除之间最大编程次数 `NOP`、
每扇区最大成功擦除次数 `E`（均不小于 1）。地址为器件内字节偏移，总容量 `S·Z`，
初始内容全为 `0xFF`，所有计数为 0。

### 位语义

- **编程** `Program(addr, data)`：写入后字节 = 原字节 `&` 数据字节，即只能把位从
  1 写成 0。数据中为 1 而原字节该位为 0（试图把 0 写回 1）属于非法位，整个操作被拒。
- **擦除** `Erase(sec)`：整个扇区恢复全 `0xFF`。
- **被打断的擦除** `ErasePartial(sec, k)`：模拟擦除途中断电，仅扇区内偏移小于 `k`
  的字节恢复为 `0xFF`，其余字节保持不变；`k = Z` 时与 `Erase` 完全等价，`k = 0`
  时只增加擦除计数。

### 计数与寿命

- 一次编程对它触及的每一页各计一次（不论触及几个字节、内容是否变化），允许跨页、
  跨扇区；任一触及页计数已达 `NOP` 时整体拒绝（报页号最小者）。
- 每次成功的 `Erase` / `ErasePartial` 使扇区擦除计数加 1，仅当计数小于 `E` 时成功
  （第 `E` 次成功，第 `E+1` 次被拒）。
- 擦除（含被打断的擦除）清零编程计数时，`Erase` 清整扇区，`ErasePartial` 只清
  「整页都落在前 `k` 字节内」的页；`k` 落在页中间时该页计数不清。
- 任何被拒的操作不改变任何字节与任何计数；错误按固定顺序只报第一个（编程：空
  data → 越界 → 计数满 → 非法位；擦除：越界 → 寿命已尽），原因可用 `errors.Is`
  区分。

### 并发与可复现性

所有方法可并发调用，结果等价于某个串行顺序；任意时刻页计数不超过 `NOP`、扇区
擦除计数不超过 `E`，字节自最近一次擦除（含被打断擦除的覆盖）后只会把位从 1 变
成 0；相同操作序列重放得到完全相同的内容、计数与错误。

### 本地验证

```bash
# 全部单元测试（含与逐字节朴素模拟的随机对照、并发不变量检查）
go test -race -v ./norflash

# 只看对照测试的逐步输入/输出/判定日志
go test -v -run TestAgainstNaiveModel ./norflash
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
