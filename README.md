# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## intelhex：Intel HEX 记录行增量加载器

`intelhex` 包把 Intel HEX（I8HEX/I32HEX）文本记录增量还原成稀疏内存映像，
绝对地址、校验和、边界与重叠冲突的判定均可精确复现。

### 记录格式

一行以冒号 `:` 开头，后接十六进制字符（大小写均可），不含换行符：

```
: LL AAAA TT DD..DD CC
```

- `LL`：数据字节数（1 字节）。
- `AAAA`：16 位偏移（大端）。
- `TT`：记录类型（1 字节）。
- `DD..DD`：`LL` 字节数据。
- `CC`：校验和，使从 `LL` 到 `CC` 全部字节之和模 256 为 0（进位回绕）。

记录类型：

- `00` 数据：`LL` 可为 0；此时不占字节、不产生映像、不做重叠判定，但 `AAAA+LL` 边界检查照常。
- `01` 文件结束：`LL` 与 `AAAA` 均须为 0。
- `02` 扩展段地址：`LL=2`、`AAAA=0`；基址 = 16 位值左移 4 位。
- `04` 扩展线性地址：`LL=2`、`AAAA=0`；基址 = 16 位值左移 16 位。
- `03`/`05` 起始地址：`LL=4`、`AAAA=0`；只记录 4 字节原始值，不进映像，后一条覆盖前一条。

### 地址计算与边界

- 基址初始为 0；`02` 与 `04` 互相覆盖，始终以最近一条为准。
- 数据记录绝对地址 = 基址 + `AAAA`，覆盖半开区间 `[绝对地址, 绝对地址+LL)`；段基址加偏移不做回绕。
- `AAAA+LL > 0x10000` 即越过 64KiB 边界，必须拒绝；恰好等于 `0x10000` 允许（末端正好落在边界）。
- 任何字节位置被第二次写入都算重叠，即使写入的值与已有值相同。
- `Segments()` 返回按地址升序的连续段，首尾相接的段合并为一段；返回的是防御性副本。

### 拒绝优先级

同一行只报第一个命中的原因，可用 `errors.Is` 区分（错误均包装为 `*LineError`，携带从 1 起的行序号，被拒绝的行也占序号）：

1. 语法：缺冒号、非十六进制字符、奇数个字符、实际数据长度与 `LL` 不符。
2. 校验和：`LL..CC` 之和模 256 不为 0。
3. 未知类型。
4. 该类型的 `LL` 或 `AAAA` 不合规。
5. 越过 64KiB 边界。
6. 与已有数据重叠。
7. 文件结束记录之后的行（数据记录的边界与重叠检查优先于此项）。
8. `Finish` 时尚未见到文件结束记录（返回裸 `ErrNotFinished`）。

被拒绝的行不改变基址、映像、起始地址与文件结束状态。`AddLine` 与查询可并发调用，
内部以互斥保证结果等价于某个串行顺序；相同行序列重放得到完全相同的映像与错误。

### 用法

```go
l := intelhex.New()
if err := l.AddLine(":10010000214601360121470136007EFE09D2190140"); err != nil {
    var le *intelhex.LineError
    errors.As(err, &le)
    log.Fatalf("line %d: %v", le.Line, err)
}
if err := l.AddLine(":00000001FF"); err != nil { // EOF
    log.Fatal(err)
}
if err := l.Finish(); err != nil {
    log.Fatal(err)
}
for _, s := range l.Segments() {
    fmt.Printf("%08X: %X\n", s.Start, s.Data)
}
```

### 本地验证

```bash
# 详细日志（输入、输出、判定依据）与竞态检测
go test -race -v ./intelhex

# 全量测试、覆盖率、vet、格式检查
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
go vet ./...
gofmt -l .
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
