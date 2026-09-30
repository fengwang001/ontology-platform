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

## extent：支持克隆共享的文件区段映射器

`extent` 包把文件的逻辑字节区间映射到物理空间，支持覆盖写、区间克隆与截断。

### 数据模型

- 每个文件是一组互不重叠的左闭右开逻辑区间 `[start, start+length)`，每段记录物理起点 `phys`。
- 未映射区间是空洞：读出零字节，不占物理空间。
- 物理空间按字节记录引用计数并保存实际内容；分配取能容纳整个区间的最低地址，
  因此同一操作序列得到完全相同的映射与物理布局。

### 劈分与合并规则

- 覆盖写先为整个新区间分配新物理空间，再释放被覆盖的旧空间；
  部分重叠的旧区段被劈开，未覆盖的左右两段保留。
- 插入新区段时，仅当相邻区段逻辑与物理都相接（`prev.end == next.start`
  且 `prev.phys + prev.length == next.phys`）才合并，保证映射表示唯一。
- 截断落在区段中间时把区段劈短，超出新长度的部分立即释放。

### 引用计数口径

- 每个物理字节的引用计数恒等于映射到它的逻辑字节数（同一物理字节可被多个
  逻辑区间共享）。
- 写入新分配的字节计数为 1；克隆共享时 +1；覆盖或截断释放时 -1，归零即回收。
- 已用物理字节数恒等于计数非零的物理字节数。

### 克隆覆盖语义

- `Clone(src, srcOff, dst, dstOff, n)` 把源区间内的映射复制到目标偏移处，
  只共享物理空间、增加计数，不复制数据。
- 目标区间原有内容按覆盖写处理（先共享、再释放旧空间）。
- 源中的空洞在目标中仍是空洞；克隆后覆盖源文件不影响目标读到的内容。

### 错误与原子性

下列情况整体拒绝且可区分（`errors.Is` 判定），被拒绝的操作不改变任何映射、
计数或已分配空间：

- `ErrEmptyRange`：区间为空或颠倒；
- `ErrOutOfBounds`：超出文件长度上限；
- `ErrOverlap`：同一文件内源与目标区间重叠的克隆；
- `ErrNoSpace`：物理空间不足。

### 并发

不同文件的读写与克隆可并发；同一文件的修改经文件写锁串行化；
读者持读锁，只能看到某次修改之前或之后的完整内容。

### 本地验证

```bash
# 全部用例（日志打印输入、输出与判定依据）
go test ./extent/ -v

# 竞态检测
go test ./extent/ -race -count=1
```
