# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 移动操作日志重放器

移动操作定义为 `Op(ts, rep, node, parent, name)`，实现在 `ontology/replayer.go`。

- **全序键**：使用 `(ts, rep)` 升序排列；`rep` 按 Go 字符串字节序比较，键相同即同一操作。
- **节点规则**：`0` 是根，`1` 是回收站；二者永不移动。用户节点 `node >= 2` 在第一个生效移动中创建。
- **跳过规则**：父节点不存在，或从 `parent` 自身沿父链向上能到达 `node` 时跳过；跳过操作仍保留在日志中。
- **撤销重做**：`Apply` 插入新键后等价于回到基线树、重放较小键、应用新操作、再重放较大键；`Changed` 返回生效标记翻转的较大键，非导出 `redone` 按较大键条目数累加。
- **稳定水位**：`Ack(rep,t)` 声明副本 `rep` 已收到所有 `ts <= t` 的操作；`stable` 是所有副本 Ack 的最小值。水位前进时，将 `ts <= stable` 的日志按全序固化进基线并从可变日志移除。
- **收敛性**：判定只依赖当前基线和按键升序的日志，不依赖到达顺序；环检测只沿目标父链走到根，步数不超过当时树节点数。
- **拒绝优先级**：参数非法、未知副本 `ErrUnknownRep`、`ts <= stable` 的 `ErrStale`、重复键 `ErrDuplicate`。

测试包含题设两种到达顺序、自环、晚到父节点激活子操作、回收站往返、并发移动胜负、稳定水位边界、折叠后的 `Changed`、Ack 回退，以及 2000 组随机操作与双到达次序对朴素全序重放的对照。

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

# 本仓库在当前环境若使用只读 HOME，可指定临时 Go 缓存
GOCACHE=/tmp/ontology-go-cache go test ./...

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
