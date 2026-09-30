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

## 流依赖树份额分配器

`streamtree` 包提供并发安全的流依赖树与字节额度分配器，虚拟根编号为 `0`。

### API

- `New(w io.Writer) *Allocator`：创建分配器；`w` 非空时记录每次调用的输入、输出和判定依据。
- `Open(id, parent, weight int, exclusive bool) error`：开启新流。
- `Reset(id, newParent, newWeight int, exclusive bool) error`：重设依赖与权重。
- `Close(id int) error`：关闭流并处理其子流。
- `Allocate(quota int, ready ...int) (map[int]int, error)`：按就绪流集合分配字节。

### 挂接规则

- 流编号必须是从未使用过的正整数；父必须是 `0` 或当前存活的流；权重必须在 `[1,256]`。
- 非独占开流：新流成为父的普通子流。
- 独占开流：新流先成为父的唯一子流，父原有的全部直接子流连同各自子树改挂到新流之下。
- 重设依赖时，若新父是该流后代，先把“新父”整棵子树改挂到该流原父下，并保持新父权重；随后再按普通或独占规则挂接该流。
- 拒绝顺序固定为：开流依次检查编号、父、权重；重设依次检查流、新父、新父为自身、权重；关闭检查流是否存在；分配先检查就绪流是否存在，再检查额度是否为负。被拒绝的操作不会改变树。

### 关闭规则

关闭流 `x` 时，其所有直接子流改挂到 `x` 的父。每个子流的新权重为：

```text
max(1, floor(weight(x) * weight(child) / sum(weight(sibling))))
```

其中分母是被关闭流全部直接子流的权重之和。已关闭编号不会复用。

### 分配规则

- 从虚拟根递归处理；只考虑包含至少一个就绪流的子树，不包含就绪流的兄弟不参与同层权重求和。
- 当前流自身就绪时，收到该子树获得的全部额度；其后代全部得到 `0`，且不会继续向下分配。
- 当前流未就绪时，在含就绪流的子流间按权重分配：
  - 基础份额是 `floor(T * w / Σw)`。
  - 若分配基础份额后仍有剩余，按 `T*w mod Σw` 从大到小补一字节。
  - 余数相同取编号较小者，保证相同输入序列重放结果完全一致。
- 无就绪流时结果为空 map（所有流份额均视为 `0`）；份额之和恒等于输入额度。
- 树变更使用排他锁，分配使用读锁；每个流始终恰有一个父，且依赖关系无环。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测与详细日志
go test -race -v ./...

# 静态检查
go vet ./...
```

若当前容器的 `go` 不在 `PATH` 中，可临时加 `PATH=/usr/local/go/bin:$PATH`；若默认 Go 缓存目录只读，可加 `GOCACHE=/tmp/ontology-go-cache`。

测试覆盖独占插入、重设到自己的后代、关闭时按比例重分且下限为 `1`、余数并列取小编号、就绪父阻断后代、无就绪流、拒绝不变更树和并发调用。
