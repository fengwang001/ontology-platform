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

## 协同编辑锚点追踪器

锚点实现位于 `ontology/anchor`，文档位置使用字符之间和两端的缝隙，缝隙编号为 `0..L`。

### 偏向与依附

- 点锚点 `AddPoint(pos, bias)` 有两种偏向：`Left` 依附左侧字符（`0` 号缝隙依附文首），`Right` 依附右侧字符（`L` 号缝隙依附文末）。
- 区间锚点的端点是带偏向的点：`Tight` 使用起点 `Right`、终点 `Left`；`Loose` 使用起点 `Left`、终点 `Right`。
- 每次编辑后先映射两个端点；若起点映射到终点右侧，则把起点改为终点，区间立即塌缩。`Collapsed` 由当前 `s == e` 判定，不是持久状态。

### 编辑映射

- `Replace(p, d, n)` 删除 `[p,p+d)` 并插入 `n` 个字符：`x<p` 不变，`x>p+d` 平移 `-d+n`；删除区及其边界缝隙按偏向取插入区前缝隙 `p` 或后缝隙 `p+n`。纯插入时，`x=p` 的 `Left` 留在插入前、`Right` 到插入后。
- `Move(p, len, q)` 只改变字符块位置，不改变长度。若目标在源后方，先把目标缝隙折算为 `q-len`；源块内部缝隙按块平移，源块外缝隙先扣除块长度，再按其相对目标缝隙的位置平移；落在目标缝隙的锚点按 `Left`/`Right` 选择插入块前或后。
- 参数不合法返回 `ErrInvalid`；长度超过 `MaxLen` 返回 `ErrTooLarge`，且非法参数优先于超长。被拒编辑不改变修订、长度或锚点。

### 历史与压缩

- 成功编辑返回递增的新修订号；锚点只能在当前修订创建，id 单调递增且删除后不复用。
- `Pos(id, asRev)` 与 `Range(id, asRev)` 从该锚点的检查点物化位置开始，重放到目标修订；编辑提交本身不遍历锚点。
- 查询错误优先级为：`ErrNoAnchor`、类型不匹配的 `ErrWrongKind`、未来修订 `ErrFuture`、早于创建修订 `ErrNotYet`、已被压缩 `ErrCompacted`。
- `Compact(newFloor)` 只接受 `floor <= newFloor <= rev`。它先把检查点更早的现存锚点物化到 `newFloor`，再删除更早编辑；创建修订不小于 `newFloor` 的锚点不额外重放。包内非导出的 `replayed` 用于精确验证重放总量。

### 锚点包验证

```bash
# 全量测试
go test ./ontology/anchor

# 竞态检测
go test -race ./ontology/anchor

# 随机 2000 组朴素模型对照；-v 会打印每组输入、返回和判定依据
go test -run TestRandomAgainstCharacterAttachmentModel -v ./ontology/anchor
```

若环境的默认 Go 构建缓存目录只读，可使用临时缓存：

```bash
GOCACHE=/tmp/go-cache go test ./...
```
