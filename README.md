# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 列页混合编码写入器（`./ontology`）

`ontology.Writer` 把无符号整数列按页编码，每页字节与解码结果可精确复现。

### 字典与降级规则

- 写入器持有列块字典：值按首次出现次序分配从 0 起的索引，已分配索引永不改变。
- `Flush` 判定次序：
  1. 若粘滞降级标记 `fallback` 已为真，直接出 Plain 页（不动 `miss`）。
  2. 试算合并后不同值个数 `D`（本页新值按本页首次出现次序接在字典之后）。`D > maxDict`（恰等不超）则置 `fallback`、字典不变、出 Plain 页（不动 `miss`）。
  3. 否则按宽度 `w` 做混合编码得 `H`，记新增字典项数 `N`：Dict 页大小 `1 + len(H) + 4×N` 严格小于 Plain 页大小 `4×Rows` 才出 Dict 页、提交字典并清零 `miss`；否则出 Plain 页、字典不变、`miss` 加 1，`miss` 连续达到 3 时置 `fallback`（其间出过 Dict 页则清零）。
- 落选页的新值不进字典，之后页再出现时按当时次序重新分配索引；降级后各页恒为 Plain 且字典不再增长。

### 宽度推导

`D=1` 时 `w=0`；其余 `w = bits.Len(D-1)`（`D=2` 得 1，`D=256` 得 8，`D=257` 得 9）。

### 游程切分与借位规则

把索引序列分成相邻值相等的极大段，维护待出文字缓冲 `P`，依次处理每段（值 `x`、长度 `L`）：

- 若 `L ≥ 8`：令 `f = (8 − |P| mod 8) mod 8`；当 `L−f ≥ 8` 时，把 `f` 个 `x` 并入 `P` 后将 `P` 作为位打包游程写出（`P` 非空时），剩余 `L−f` 个写成 RLE 游程；否则整段并入 `P`。
- 若 `L < 8`：整段并入 `P`。序列结束时 `P` 非空则写成位打包游程。
- RLE 游程：头 `varint(计数<<1)` + 重复索引值（`ceil(w/8)` 字节小端，`w=0` 占 0 字节）。
- 位打包游程：头 `varint(组数<<1 | 1)` + `组数×w` 字节，每组 8 个值各 `w` 位，先出现的值在低位、字节小端；仅最后一个位打包游程的末组允许用索引 0 补足 8 个。

### 页格式与大小比较

- Plain 页：`Data` 为各值 4 字节小端，`Width=0`、`DictLen=0`。
- Dict 页：`Data` 为 1 字节 `w` 后接混合编码流 `H`；`4×N` 的新增字典项成本只计入大小比较、不写入 `Data`（字典由写入器持有），`DictLen` 为提交后的字典长度。
- `Decode` 用已提交字典还原页：校验首字节宽度、游程头（计数/组数非零）、值个数恰为 `Rows`、索引小于 `DictLen`、补足位全为 0、无多余字节，违例报 `ErrCorrupt`。

### 本地验证

```bash
go test ./ontology/                 # 规则单测 + 2000 组随机对照朴素模拟
go test -race -v ./ontology/        # 竞态检测；随机用例打印输入/输出/判定依据
go test -run TestRandomAgainstNaive -v ./ontology/   # 仅随机对照，查看判定日志
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
