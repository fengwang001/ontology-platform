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

## 虚拟同步成员视图安装器

实现在 `membership/installer.go`。

- `NewInstaller(initialMembers)` 要求非空、成员标识非空且互不相同；初始视图编号为 1，并进入稳定态。
- `BeginChange(newMembers)` 中，幸存者是 `oldMembers ∩ newMembers`，新加入者是 `newMembers - oldMembers`。新成员集合不能与旧集合完全相同。
- 准入判据使用旧视图多数：`2 × 幸存者数 > 旧视图成员数`。新加入者不计入这个判据。
- 只有幸存者可以调用 `Report(member, counts)`；`counts` 表示该成员对每个旧成员发送者已连续交付到的最大序号，缺省为 0，发送者必须属于旧视图。
- `Complete()` 要求所有幸存者都已提交。对每个旧成员发送者，即使该成员不会留在新视图，`agreed[s]` 都取所有幸存者报告中的最大序号。
- 每个幸存者的补交付列表按发送者标识字典序排列；同一发送者内从 `counts[s] + 1` 到 `agreed[s]` 按序号升序排列。新加入者没有补交付，也不出现在 `CatchUp` 中。
- 安装后视图编号加 1，新成员按标识字典序写入计划，安装器切换到以新成员为旧成员的稳定态，新视图内所有已交付计数重新从 0 开始。
- `Abort()` 清空本次变更中的幸存者、候选成员与报告并回到原稳定态，视图编号不变。
- 所有方法由互斥保护，返回的 `Plan` 及其切片、映射均为深拷贝；相同操作序列以不同 `Report` 到达顺序重放，会得到逐字段相同的计划。

错误按以下顺序只返回第一个：

- `BeginChange`：已在变更中、新成员为空、新成员重复、存在空标识、新成员集合相同、幸存者不满足旧视图多数。
- `Report`：不在变更中、提交者不是幸存者、提交者已报告、存在不属于旧成员的发送者。
- `Complete`：先检查不在变更中，再检查仍有幸存者未提交。
- `Abort`：不在变更中。

被拒绝的操作不会修改成员、视图编号、已有报告或稳定态。

### 本地验证

如果 `go` 不在 `PATH` 中，可使用本机的 `/usr/local/go/bin/go`；如果默认 Go 构建缓存只读，指定临时缓存：

```bash
GOCACHE=/tmp/go-cache go test -v ./...
GOCACHE=/tmp/go-cache go test -race ./...
GOCACHE=/tmp/go-cache go vet ./...
```

`TestRandomScenariosAgainstMessageSetUnion` 会打印随机输入、输出计划和判定依据，并与“把所有幸存者已交付的消息逐条展开后求并集”的朴素实现逐字段比较。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
