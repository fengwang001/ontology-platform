# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 列块页编码（`ontology` 包）

`ontology.Writer` 把一列 `uint32` 按页编码为“列块字典 + 游程/位打包”混合格式。

### 构造与缓冲

- `New(maxDict, maxRows)`：要求 `1 ≤ maxDict ≤ 65536`、`1 ≤ maxRows ≤ 65536`，否则返回 `ErrParam`。
- `Append(v)`：缓冲达到 `maxRows` 时返回 `ErrFull`，且不改变任何状态。
- `Flush()`：缓冲为空返回 `ErrEmpty`；否则把缓冲行编码成一页并清空缓冲，返回 `Page{Enc, Rows, Width, DictLen, Data}`。
- 全部方法可并发调用（内部互斥），被拒绝的操作不改变缓冲、字典与降级标记。

### 字典与降级

字典按值首次出现次序分配从 0 起的索引，已分配索引永不改变。写入器还持有粘滞标记
`fallback`（初始假）与落选计数 `miss`（初始 0）。`Flush` 的判定次序为：

1. `fallback` 已为真：直接出 Plain 页，不读字典、不动 `miss`。
2. 把本页新值按首次出现次序试算接在字典之后得到不同值个数 D。`D > maxDict`（恰等不超）时置
   `fallback = true`，字典保持不变，出 Plain 页，不动 `miss`。
3. 否则按宽度 w 做混合编码试算 H，记试算新增项数 N：
   - Dict 页大小 `1 + len(H) + 4*N`，Plain 页大小 `4*Rows`。
   - **严格小于**才出 Dict 页：提交新字典项、`miss = 0`。
   - 相等或更大则出 Plain 页：字典不变、`miss++`；`miss` 连续达到 3 时置 `fallback = true`。
     其间任何一次 Dict 页都会把 `miss` 清零。

落选页出现的新值不会进入字典，后续页再次出现时仍按“新值”重新分配索引。

### 宽度推导

- `D = 1` 时 w = 0；否则 `w = bits.Len(D-1)`。
- 即 `D = 2^k` 时 w = k，`D = 2^k + 1` 时 w = k+1（如 256→8、257→9）。

### 混合游程切分与借位

把索引序列切成相邻相等值的极大段（值 x、长度 L），维护待出文字缓冲 P：

- `L ≥ 8` 时令 `f = (8 - |P| mod 8) mod 8`：
  - 若 `L - f ≥ 8`：先把 f 个 x 借入 P（P 非空则整体作为位打包游程写出），
    剩余 `L-f` 个写成 RLE 游程；
  - 若 `L - f < 8`：整段并入 P。
- `L < 8`：整段并入 P。
- 序列结束时 P 非空则写成一个位打包游程。

游程头均为无符号 varint：

- RLE：`varint(count << 1)` 后接索引值，占 `ceil(w/8)` 字节小端（w=0 占 0 字节）。
- 位打包：`varint(groups<<1 | 1)` 后接 `groups*w` 字节；每组 8 个值、每值 w 位，
  先出现的值在低位、字节小端；仅最后一个位打包游程的末组可用索引 0 补足。

### 页字节

- Plain：`Data` 为各值 4 字节小端；`Width = 0`、`DictLen = 0`。
- Dict：`Data` 为 1 字节 w 后接混合编码 H；`4*N` 只参与大小比较，不写入页中
  （字典由写入器持有）。`DictLen` 为提交后的字典长度。

### 解码与校验

`Decode(p)` 用已提交字典还原值序列。Plain 页要求 `len(Data) == 4*Rows`；Dict 页校验
首字节等于 Width、游程头合法（计数/组数非零且不超行数）、索引小于 DictLen、末组补位全为 0、
解出值个数恰为 Rows、无多余字节，否则返回 `ErrCorrupt`。字典索引稳定，因此历史 Dict 页
随时可解码。非导出计数器 `touches` 记录编码时读取的索引个数，每页恰为 Rows（≤ 2×Rows）。

### 本地验证

```bash
# 全量测试（含 2000 组随机输入与朴素模拟对照、解码往返、并发）
go test ./...

# 竞态检测
go test -race ./...

# 查看随机对照用例的输入 / 输出 / 判定依据日志
go test -run TestRandomAgainstNaiveSimulation -v
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
