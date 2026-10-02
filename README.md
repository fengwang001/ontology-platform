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

## 因果稳定墓碑回收器（ontology 包）

基于版本向量的墓碑回收：判定删除产生的墓碑何时能被安全回收而不丢更新。

### 版本向量比较

- 每次写入或删除携带一个版本向量，长度为副本数，分量非负。
- 向量按**分量顺序的字典序**比较：从首个分量起逐一比较，第一个不相等的分量决定大小。
- 每个键只保留当前赢家条目（向量、值、是否墓碑）：事件向量**严格大于**赢家向量时覆盖；
  相等或更小者视为迟到/重复事件，一律忽略，不改变存储。
- 无论事件是否被忽略，**副本时钟都照常逐分量取最大**——该副本至少产出了或追平了这个向量。

### 稳定向量与回收判定

- **稳定向量** = 各副本时钟逐分量取最小值。时钟只增，故稳定向量单调不减。
- **回收规则**：`Reclaim` 移除所有「是墓碑 且 向量逐分量 ≤ 稳定向量」的条目，
  返回被移除的键列表（按字典序），并追加到已回收列表。
- 已回收键的墓碑向量被保留为判定水位：迟到的旧事件向量不大于该水位时仍被忽略，
  **旧事件不会复活已删键**；只有向量严格更大的新事件才能重新建立条目。

### 非法输入

以下情形分别返回互不相同的可判定错误（`errors.Is` 判定），且任一批内任一条被拒则整批不生效，
存储、时钟、稳定向量与已回收列表均不变：

- `ErrNonPositiveReplicas`：副本数非正（构造时）
- `ErrReplicaOutOfRange`：副本编号越界
- `ErrNegativeComponent`：向量含负分量
- `ErrEmptyKey`：键为空
- `ErrVectorLengthMismatch`：向量长度与副本数不一致

### 并发

`StableVector`、`Reclaim`、`View`、`SelfCheck` 均可并发调用；只读接口共享读锁，
并发只读同一实例得到的视图逐字段相同。

### 本地验证

```bash
# 全量测试（含竞态检测与输入/结果/判定依据日志）
go test -race -v ./ontology

# 静态检查
go vet ./...
gofmt -l .
```
