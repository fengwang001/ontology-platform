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

## Change Buffer

`changebuffer` 包为不在缓冲池中的二级索引页缓存 `Insert`、`DeleteMark` 和 `Purge`。页大小为 `S`，页内已用空间 `used` 是全部条目尺寸之和，空闲空间为 `F=S-used`。

### 空闲桶与保证下界

`bucket(F)` 按当前空闲空间选择：

- `32F < S`：桶 `0`，保证下界 `lb(0)=0`
- `16F < S`：桶 `1`，保证下界 `lb(1)=floor(S/32)`
- `8F < S`：桶 `2`，保证下界 `lb(2)=floor(S/16)`
- 其他情况：桶 `3`，保证下界 `lb(3)=floor(S/8)`

等号归入更大的桶：例如 `32F=S` 不属于桶 `0`，而属于桶 `1`。

### 缓冲与强制合并

页在池中时，`Op` 直接施加到页上。页不在池中时，只有同时满足以下条件才缓冲：

- 该页队列长度小于每页上限 `Kp`
- 非 `Insert` 不占字节额度
- `Insert` 满足 `bufBytes+e <= lb(bucket(F))`
- `Insert` 还满足全部不在池页的缓冲字节总和加 `e` 不超过全局上限 `G`

不满足任一缓冲条件时走强制合并：先按到达序施加该页队列中的操作并清空队列、释放全局额度，再把页置为池中并直接施加当前操作。合并本身不会因空间不足失败；若当前直接施加失败，整个 `Op` 被拒绝，页仍不在池中，条目、队列、计数全部保持原样。

`Load` 对不在池中的页执行相同合并并读入池；页已在池中时无变化。`Evict` 只允许对池中的页调用，调用后保留条目并允许后续操作重新缓冲。

### 条目语义

所有缓冲操作都严格按到达序施加，直接施加与合并共用同一套语义：

- `Insert(k,e)`：键已存在时清除删除标记且保留原尺寸；键不存在时只有 `F >= e` 才能加入尺寸为 `e` 的条目。
- `DeleteMark(k)`：键存在时设置删除标记；键不存在时无变化。
- `Purge(k)`：仅当键存在且带删除标记时移除条目并释放尺寸；其他情况无变化。

`View(page)` 把队列按序逻辑施加到页条目副本上，返回按键升序排列的结果，不修改真实状态；其结果与随后执行 `Load(page)` 后得到的条目完全一致。

### 本地验证

随机朴素模型对照固定生成 2000 组操作序列，逐组逐操作比较错误、逻辑视图、条目、队列、池状态、页内 `bufBytes` 与全局计数。使用详细模式可打印每组输入、输出和缓冲或强制合并依据：

```bash
GOCACHE=/tmp/go-cache go test -race -v ./changebuffer -run TestRandomSequencesAgainstNaiveOracle -count=1
```

全量验证：

```bash
GOCACHE=/tmp/go-cache go test -race ./...
GOCACHE=/tmp/go-cache go vet ./...
```
