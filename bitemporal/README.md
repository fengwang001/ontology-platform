# bitemporal — 双时态快照版本兼容判定

判定本体实例快照在格式版本升级/降级时，有效时间轴与事务时间轴信息
能否保留；信息必然损失时精确报告损失内容；归属语义会改变时判为不兼容。

完整设计说明见 `DESIGN.md`。

## 快速使用

```go
reg := bitemporal.DefaultRegistry()
r := &bitemporal.Record{
    ID:          "obj-1",
    Valid:       &bitemporal.Interval{Start: 10, End: 20, StartBound: bitemporal.Closed, EndBound: bitemporal.Closed},
    Transaction: &bitemporal.Interval{Start: 10, End: 20, StartBound: bitemporal.Closed, EndBound: bitemporal.Closed},
}

// 仅判定
v := bitemporal.Judge(reg, r, "v1" /*双时态,闭*/, "v3" /*仅事务,闭*/)
// v.Status == bitemporal.StatusInfoLoss
// v.Lost[0].Axis == bitemporal.ValidTime  —— 明确报告损失有效时间轴

// 判定 + 投影迁移（返回新记录，不修改原记录）
m := bitemporal.Judge(reg, r, "v1", "v2") // 闭 -> 半开，点集等价，兼容

// 多跳路径
p := bitemporal.MigratePath(reg, r, []string{"v1", "v3", "v1"})
```

## 判定结论（固定优先级，从高到低）

1. `record_invalid`：记录自身区间不自洽（起点晚于终点等）；
2. `incompatible`：共享轴边界语义差异会改变某查询时点的归属（附 witness）；
3. `info_loss`：目标版本不记录某条轴，报告具体丢失的轴；
4. `unknown_version`：版本号超出注册表可识别范围。

缺失轴（源不记录、目标记录）一律按固定规则填充为永恒区间
`[MinFinite, MaxFinite+1)`，无未定义状态，无需逐例人工指定。

## 测试

```bash
go test -race ./...
BITEMPORAL_LOG=/tmp/judge.log go test ./bitemporal/ -run TestRandomDifferential -v
go test ./bitemporal/ -run TestBatchLinearComplexity -v
```
