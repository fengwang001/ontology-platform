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

## tombstone：范围删除墓碑碎片化器

`tombstone` 包登记带序号的半开范围墓碑并维护唯一的碎片表。

### 碎片切分与合并规则

- 墓碑 `(s, e, q)` 表示键范围 `[s, e)`（字节串按字节序比较）与正整数序号 `q`，可乱序、可重复登记；完全相同的墓碑重复登记也各占一个墓碑计数，不同墓碑可同序号。
- 碎片按全部墓碑端点切开为互不重叠的半开段；每段记录覆盖它的墓碑序号集合（按序号去重后降序排列）。
- 无墓碑覆盖的段不产生碎片。
- 相邻且序号集合完全相同的碎片合并为一段，因此碎片表由墓碑集合唯一确定，与登记顺序无关。

### 判定条件

`Covered(k, q, snap)`：当且仅当存在覆盖 `k` 的墓碑序号 `t` 满足 `q < t <= snap` 时为真。`t == q` 不覆盖，`t == snap` 可见；`snap < q` 时按公式恒为假，对 `q`、`snap` 不做校验。判定通过二分查找定位碎片，考察的碎片数为 O(log n)（非导出计数器经 `Examined()` 暴露，测试证明 1000 与 100000 碎片下分别为 10 与 17）。

### 登记校验

按顺序只报第一个错误，被拒绝的操作不改变碎片表与墓碑计数：

1. `ErrInvalidRange`：`s >= e`；
2. `ErrZeroSeq`：序号为 0；
3. `ErrNotConstructing`：`Cap <= 0`，非构造状态；
4. `ErrCapExceeded`：墓碑总数已达 `Cap`。

登记、判定与碎片查询均可并发调用（内部读写锁 + 惰性重建），结果等价于某个串行顺序。

### 本地验证

```bash
# 全部测试（含 2000 组随机输入与朴素逐条扫描对拍，-v 打印输入/输出/判定依据）
go test -v ./tombstone

# 竞态检测
go test -race ./tombstone

# 碎片考察数对数增长的证明
go test -run TestExaminedScaling -v ./tombstone
```
