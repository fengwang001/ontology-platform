# FTL 映射、垃圾回收与磨损均衡服务

本仓库实现一个确定性、可并发调用的闪存转换层。

## API

- `New(cfg Config) (*FTL, error)`：创建固定块数、页数、LPN 范围和水位的 FTL。
- `Write(lpn int) error`：写入或覆盖逻辑页；写入后按需执行 GC 与静态磨损均衡。
- `Read(lpn int) (PhysicalPage, error)`：返回当前映射的物理块号和页号。
- `Discard(lpn int) error`：丢弃逻辑页；未映射时无操作。
- `Stats() Stats`：逻辑写入数、物理编程数、空闲块数、退役块数和各块擦除次数。
- `WriteAmplification() (int, int)`：以 `物理编程数, 逻辑写入数` 返回整数比。
- `Blocks()` 与 `PhysicalPages()`：导出可复现物理布局所需的块和页状态。
- `NewNaive(cfg)`：独立朴素逐页模型，供测试对照。
- `NewTracer(...)`：打印每步操作输入、输出和判定依据。

错误按优先级返回 `ErrInvalidArgument`、`ErrUnwritten`、`ErrNoSpace`。

详细设计见 [DESIGN.md](DESIGN.md)。

## 环境要求

- Go 1.26+（`/usr/local/go/bin/go version` 确认）

## 测试

```bash
# 全量测试
GOCACHE=/tmp/go-build-cache-ontology /usr/local/go/bin/go test ./...

# 带竞态检测与详细操作日志
GOCACHE=/tmp/go-build-cache-ontology /usr/local/go/bin/go test -race -v ./...

# 固定种子随机对照与日志
GOCACHE=/tmp/go-build-cache-ontology /usr/local/go/bin/go test \
  -run TestRandomDifferentialWithTrace -v ./...

# 覆盖率
GOCACHE=/tmp/go-build-cache-ontology /usr/local/go/bin/go test \
  -coverprofile=coverage.out ./...
GOCACHE=/tmp/go-build-cache-ontology /usr/local/go/bin/go tool cover \
  -html=coverage.out
```

## 代码检查

```bash
/usr/local/go/bin/gofmt -l .
GOCACHE=/tmp/go-build-cache-ontology /usr/local/go/bin/go vet ./...
```
