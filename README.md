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

## 基数估计器（cardinality 包）

`cardinality` 提供支持加入与撤回、可并发使用的基数估计器。

### 稀疏转稠密的一次性

- 基数不超过阈值时处于稀疏模式，用显式集合精确计数，`Estimate()` 返回精确值。
- 加入第 `threshold+1` 个不同键时，一次性把显式集合重放进近似草图（稠密模式），此后永不回落——即使撤回到阈值以下也保持稠密模式。
- 稠密草图按确定哈希（FNV-1a + fmix64 混合）把每个键落到固定寄存器与秩上，基数估计由 `2^precision` 个寄存器的秩经固定公式（HyperLogLog + 小基数线性计数修正）算出，结果可复现。

### 撤回回落规则

- 每个寄存器维护各秩的计数：加键递增对应秩的计数，撤键递减。
- 撤键使寄存器当前最大秩被删尽时，该寄存器回落到剩余键中的最大秩，而非清零；仅当寄存器无任何剩余键时才归零。
- 撤回完全可逆：并发追加再并发撤回后，寄存器与估计值精确还原到追加前快照，不发散。

### 错误分类

非法精度（`ErrInvalidPrecision`）、非法阈值（`ErrInvalidThreshold`）、空键（`ErrEmptyKey`）、撤回不存在的键（`ErrKeyNotFound`）均整体拒绝，且一次失败不改变显式集合、寄存器与估计值。

### 本地验证：批量重算核对

`Verify()` 用当前键集合批量重算全部寄存器并与现值逐位比对，可用于核对增量更新是否正确：

```bash
# 运行全部单测（日志打印操作、模式、寄存器与判定依据）
go test -v ./cardinality

# 带竞态检测，覆盖并发加入/撤回与串行一致性核对
go test -race -v ./cardinality
```
