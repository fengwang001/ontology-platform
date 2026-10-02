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

## 时间序列压缩块

实现在 `ontology/block.go` 与 `ontology/decoder.go`：

- `New(start int64, maxBytes int) (*Block, error)` 校验 `13 ≤ maxBytes ≤ 1048576`，并先写入块头：`start` 的 64 位二补码。
- 位流始终高位在前；多字段从高位写起，`Bytes()` 返回末尾补 `0` 到整字节的副本，`Bits()` 返回有效位数，`Len()` 返回已接受样本数。
- 首个样本写 14 位 `d0 = t-start`，要求 `0 ≤ d0 ≤ 15359`，随后写 IEEE 754 `float64` 的 64 位位模式；`-0.0`、`+0.0` 与不同 NaN payload 均按原始位模式区分。
- 后续样本要求时间戳非递减。相邻间隔 `d = t-lastT`，二阶差分 `dod = d-lastD`；`d` 或 `dod` 的 int64 运算溢出，或 `dod` 超出 int32 范围，返回 `ErrDelta`。
- `dod` 四档编码为：`0 → 0`；`[-63,64] → 10 + 7 位`；`[-255,256] → 110 + 9 位`；`[-2047,2048] → 1110 + 12 位`；其余为 `1111 + 32 位`。7/9/12 位仅在无符号值严格大于正中点时转负；32 位固定按 int32 解释。
- 值 XOR 为 0 时写 `0`；非零时按 Gorilla 风格选择复用当前 `(lz,tz)` 窗口或重开。复用必须同时满足 `lz ≥ pl`、`tz ≥ pt`，且只有重开严格更短时才重开，成本相等时复用。前导零最大记为 31，有效长度 64 在 6 位长度字段中记为 0。
- `Seal()` 追加 `1111 + 32 个 0`，共 36 位；零样本块也可封口。首样本 `d0 ≤ 15359`，前 4 位不会是 `1111`，所以封口标记不会与样本前缀冲突。
- 每次 `Append` 都要求“追加后有效位数 + 36 ≤ maxBytes×8”，不足则返回 `ErrFull` 且不改变任何编码状态；因此 `Seal()` 自身不需要再失败，重复封口或封口后追加返回 `ErrSealed`。
- 错误检查顺序为：已封口 `ErrSealed`；时间戳倒退 `ErrOrder`；时间差非法 `ErrDelta`；容量不足 `ErrFull`。
- `Decode(data)` 读取 64 位起点并解码到封口标记；样本中途位不足、缺少标记、标记后有整字节以上剩余，或不足 8 位的尾部补位含 1，均返回 `ErrCorrupt`。
- `Block` 的全部公开方法使用互斥保护，并发调用等价于某个合法串行顺序；相同操作序列重放得到完全相同的位流。

### 本地验证

```bash
# 全量测试
GOCACHE=/tmp/go-cache GOPATH=/tmp/go-path go test ./... -v

# 竞态检测
GOCACHE=/tmp/go-cache GOPATH=/tmp/go-path go test -race ./...

# 仅运行 2000 组朴素位串模型随机对照
GOCACHE=/tmp/go-cache GOPATH=/tmp/go-path go test ./ontology -run TestRandomSequencesMatchNaiveBitModel -v

gofmt -l .
go vet ./...
```

随机模型测试会逐位拼接头部、四档时间戳前缀、XOR 窗口字段和封口标记，并对每个随机序列比较真实块位串、样本数、容量拒绝及解码往返；详细输入、输出位串和判定依据使用 `go test -v` 输出。
