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

## 子系统

### `reflog` — 带引用日志的对象保留与回收

提交（唯一标识、父提交、创建时刻、内容对象集合）与内容对象都记录首次写入本地的
时刻；引用的每次创建/更新/删除追加一条日志记录（旧值、新值、时刻、操作者）。

- 两档过期：记录旧值是引用当前值的祖先（含相等）走可达档，否则走不可达档；
  已删除引用的记录全部走不可达档；年龄取等即过期。
- 存活根 = 引用当前值 + 未过期记录的新旧值；从根沿父边与内容引用可达即存活，
  其余对象超过新鲜宽限（取等可删）即被回收。
- 回收与过期可独立执行，也可 `ExpireAndGC` 合并（等价于先过期后回收）。
- 查询 `ReadLog(name, index)`：序号 1 为最新；「从未存在」与「记录已全部过期」
  分别报 `ErrRefNotFound` 与 `ErrRecordNotFound`。

设计与性能论证见 [reflog/DESIGN.md](reflog/DESIGN.md)。

```bash
go test ./reflog/ -v                 # 功能与随机模型对拍
go test ./reflog/ -race              # 并发串行等价
go test ./reflog/ -run xxx -bench .  # 过期/回收开销与记录数无关的证据
```
