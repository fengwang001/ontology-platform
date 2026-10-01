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

## ihex：Intel HEX 增量加载器

`ihex` 包把文本记录行逐行还原成稀疏内存映像。

### 记录格式

一行形如 `:LLAAAATT[DD...]CC`（十六进制大小写均可，不含换行）：

- `LL`：数据长度（1 字节）
- `AAAA`：偏移地址（2 字节）
- `TT`：记录类型（1 字节）
- `DD...`：`LL` 字节数据
- `CC`：校验和（1 字节），`LL` 到 `CC` 全部字节之和模 256 必须为 0

记录类型：

| 类型 | 含义 | 约束 |
| ---- | ---- | ---- |
| 00 | 数据 | `LL` 可为 0（不占字节、不进映像、不做重叠判定，边界检查照常） |
| 01 | 文件结束 | `LL=0`、`AAAA=0` |
| 02 | 扩展段地址 | `LL=2`、`AAAA=0`，基址 = 16 位值 << 4 |
| 03 | 起始地址（段） | `LL=4`、`AAAA=0`，只记录不进映像 |
| 04 | 扩展线性地址 | `LL=2`、`AAAA=0`，基址 = 16 位值 << 16 |
| 05 | 起始地址（线性） | `LL=4`、`AAAA=0`，只记录不进映像 |

### 地址计算

- 基址初始为 0，由最近一条 02 或 04 设置，二者互相覆盖。
- 数据记录绝对地址 = 基址 + `AAAA`，覆盖半开区间 `[绝对地址, 绝对地址+LL)`。
- `AAAA+LL > 0x10000` 即越过 64KiB 边界，必须拒绝；恰好等于 `0x10000` 允许。
- 基址加偏移不做回绕。
- 任何字节位置被第二次写入（无论值是否相同）都算重叠。
- 文件结束记录之后的任何行都拒绝；`Finish` 要求已见到文件结束记录。
- 映像查询 `Segments` 返回按地址升序的连续段，首尾相接的段合并成一段。

### 拒绝优先级

只报第一个错误，原因可用 `errors.Is` 区分，错误携带从 1 起的行序号
（被拒绝的行也占序号），且被拒绝的行不改变基址、映像、起始地址与
文件结束状态：

1. 语法（`ErrSyntax`：缺冒号、非十六进制字符、奇数个字符、数据长度与 LL 不符）
2. 校验和（`ErrChecksum`）
3. 未知类型（`ErrUnknownType`）
4. 该类型的 LL 或 AAAA 不合规（`ErrBadField`）
5. 越过 64KiB 边界（`ErrBoundary`）
6. 与已有数据重叠（`ErrOverlap`）
7. 文件结束之后的行（`ErrAfterEOF`）
8. `Finish` 时无文件结束（`ErrNoEOF`）

`AddLine` 与查询可并发调用，结果等价于某个串行顺序；相同行序列重放
得到完全相同的映像与错误。

### 本地验证

```bash
# 单元测试（日志打印输入、输出与判定依据）
go test -v ./ihex

# 竞态检测
go test -race ./ihex
```
