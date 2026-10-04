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

## 三方目录同步计划器

`ontology` 包以稳定整数 id 表示条目身份，比较基线、本地、远端三份 `Snapshot`。

- **属性级三方判定**：位置是 `(Parent, Name)`，内容是文件的 `Hash`；目录没有内容属性。每个 id、每个属性独立比较相对基线的变化。仅一侧改变时推送 `Create`、`SetLoc`、`SetHash` 或 `Delete`；两侧改成相同结果则无动作；删除对修改产生 `DeleteModify`；两侧都改且不同分别产生 `Loc` 或 `Content`。冲突属性不产生动作，其他非冲突属性仍可推送。
- **目录删除否决与复活**：单边目录删除原本要向未变一侧推送 `Delete`。若该侧执行完全部动作后仍有条目以该目录为 `Parent`，则不删除目录，并在删除侧按未变侧当前位置 `Create` 该目录。判定按执行前目录深度从深到浅进行，因此子目录复活会继续阻止祖先目录删除。
- **同名消解**：分别模拟两侧动作后的最终状态；同一 `(Parent, Name)` 下保留最小 id，其余按 id 升序改名为 `Name + ".c" + id`。若新名字仍冲突，则持续追加 `x`。新建条目直接以新名字 `Create`，已有条目则体现为 `SetLoc`。最终仍有重名、悬挂父 id 或环时返回 `ErrMergeInvalid`。
- **动作排序**：每侧先执行 `Create`（执行后深度升序、同深度 id 升序），再执行 `SetLoc` 与 `SetHash`（id 升序，同 id 先 `SetLoc`），最后执行 `Delete`（执行前深度降序、同深度 id 升序）。冲突按 id 升序，同 id 顺序为 `DeleteModify`、`Loc`、`Content`。
- **基线提交**：`Commit` 先执行 `Plan`；存在冲突时返回 `ErrHasConflicts` 且不更新基线。无冲突时基线更新为本地快照执行 `ToLocal` 后的状态。输入非法时按 `ErrBadLocal`、`ErrBadRemote`、`ErrMergeInvalid` 的优先级返回；任何拒绝调用都不改变基线。

计划结果使用固定枚举值并复制输入/输出快照；`Plan` 使用读锁、`Commit` 使用写锁，可并发调用。相同输入使用固定随机种子之外的无随机算法，重放得到相同清单。

### 本地验证

本环境如果未把 Go 加入 `PATH`，可显式使用：

```bash
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/tmp/go-cache

go test -v ./ontology
go test -race ./...
go vet ./...
gofmt -l .
```

随机对照测试固定生成 2000 组合法三份快照；`go test -v ./ontology -run TestRandom2000CompareNaive` 会逐组打印 JSON 格式的输入、输出和判定依据，并与独立朴素流水线对照。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
