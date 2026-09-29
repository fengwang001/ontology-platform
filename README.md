# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## segmap：支持克隆共享的文件区段映射器

`segmap` 包（`segmap/`）把文件的逻辑字节区间映射到统一的物理空间，
支持覆盖写、区间克隆（共享物理空间）与截断。所有区间均为左闭右开。

- **映射表示**：每个文件持有一组有序、互不重叠的区段 `(logicalStart, physicalStart, length)`。
- **空洞**：未映射逻辑区间不占物理空间，读出为零字节。
- **物理分配**：first-fit，取能容纳请求的最低地址；物理空间用“等计数 run”表示。

### 劈分规则

- 覆盖写与截断前，先在操作区间的两个边界处劈开旧区段。
- 一次写入覆盖旧区段中部时，旧区段被劈成左、中、右三段，中段被替换，左右保留。
- 写入顺序固定为：先为整个新区间分配新物理空间（计数置 1）并插入新映射，
  再对操作前快照中被覆盖的物理字节计数 -1。这保证分配失败时在触碰任何映射前拒绝，
  且新映射自身绝不会被误释放。

### 合并规则（表示唯一）

- 相邻区段**逻辑端点相接**且**物理端点也相接**时必须合并为一条。
- 仅逻辑相接、物理不相接（克隆共享造成）时保持独立，不得合并。
- 物理侧相邻 run 计数相同也立即合并；空闲字节（计数 0）并入空闲池。

### 引用计数口径

- 每个物理字节的引用计数恒等于映射到它的逻辑字节数：一条长度 n 的映射
  对应 n 个物理字节各 +1，解除映射各 -1。
- 计数归零的物理字节立即回收，可被后续 first-fit 重新分配（优先最低地址）。
- 全局恒等式：`Σ物理字节计数 == 全部文件已映射逻辑字节总数`；
  `已用物理字节 == 计数非零的物理字节数`（共享时二者不相等，属正常）。

### 克隆覆盖语义

- `Clone(src, srcOff, dst, dstOff, len)` 只复制映射、不复制物理字节：
  源区段对应物理字节计数 +1，源中的空洞在目标仍是空洞（不产生映射）。
- 目标区间原内容按覆盖写处理：先对源片段计数 +1，再释放目标被覆盖映射。
- 同一文件内源区间与目标区间重叠时整体拒绝（恰好相接不算重叠）。

### 失败原因（可经 `errors.Is` 区分，且拒绝无副作用）

- `ErrInvalidRange`：区间为空、端点颠倒或为负数。
- `ErrFileTooLarge`：区间超过 `MaxFileLength`，或截断长度非法。
- `ErrSameFileOverlap`：同文件克隆的源/目标区间相互重叠。
- `ErrSpaceExhausted`：first-fit 找不到足够的连续空闲物理字节。

### 并发与确定性

- 不同文件的读写与克隆可完全并发；同一文件的修改串行（每文件 `sync.RWMutex`），
  读者只看到某次修改之前或之后的完整内容；跨文件克隆按文件名固定加锁顺序避免死锁。
- 物理池独立加锁；同一操作序列重复执行得到完全相同的映射与物理布局。
- 每次操作都通过日志打印输入、输出与判定依据（默认写 `os.Stderr`，
  可用 `SetLogger` 注入）。

### 本地验证

```bash
# segmap 包全部测试（含竞态检测；测试内打印操作日志）
go test -race -v ./segmap

# 仅看不变量与关键场景
go test -race -run 'TestWriteSplits|TestCloneThen|TestTruncate|TestReclaimed|TestAdjacent|TestHoles' -v ./segmap

go vet ./...
gofmt -l .
```

测试覆盖：一次写入把旧区段劈成三段、克隆后覆盖源文件一半而克隆内容不变、
截断落在区段中间、回收字节数与最低地址复用、双相接合并/仅逻辑相接不合并、
空洞读零与空洞克隆、各类拒绝原因与原子性、跨文件并发与读快照、布局确定性。
`Store.CheckInvariants` 在每个用例末尾校验上述全部口径。

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
