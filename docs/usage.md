# 实例存储与聚合视图 — 使用说明

```go
package main

import (
	"fmt"

	"ontology/ontology"
)

func main() {
	types := []ontology.ObjectType{{
		Name: "Order",
		Attrs: map[string]ontology.AttrSpec{
			"region":   {Name: "region", Type: ontology.AttrString, Required: true},
			"category": {Name: "category", Type: ontology.AttrString, Required: true},
			"amount":   {Name: "amount", Type: ontology.AttrInt, Required: true},
		},
	}}
	views := []ontology.ViewSpec{{
		Name: "AmountByRegion",
		Kind: ontology.AggSum,
		Sources: []ontology.SourceSpec{{
			Type: "Order", GroupAttr: "region", ValueAttr: "amount", GroupValid: true,
		}},
	}}

	s := ontology.NewStore(types, views)

	// 创建：Expected=0
	w, err := s.Write(ontology.WriteRequest{
		Type: "Order", Key: "o1",
		Attrs:    map[string]any{"region": "east", "category": "book", "amount": 100},
		Expected: 0,
	})
	// 更新/迁移：Expected 必须等于当前版本
	_, _ = s.Write(ontology.WriteRequest{
		Type: "Order", Key: "o1",
		Attrs:    map[string]any{"region": "west", "category": "book", "amount": 120},
		Expected: w.Version,
	})

	r := s.Query("AmountByRegion", "west")
	fmt.Println(r.Exists, r.Count, r.Value) // true 1 120

	// 删除：贡献在生效时刻从所有视图扣除
	_, _ = s.Delete(ontology.DeleteRequest{Type: "Order", Key: "o1", Expected: w.Version + 1})

	// 全量校验：从源实例重新计算并与增量索引比对
	if err := s.Verify(); err != nil {
		panic(err)
	}

	// 用已接受提交日志在空存储上确定性重放
	replica := ontology.Replay(types, views, s.Journal())
	_ = replica
}
```

## 错误处理

所有失败返回 `*ontology.OpError`，`Code` 三选一：

- `ErrInvalidArgument`（参数非法，最先判定）
- `ErrVersionConflict`，并带 `Reason`：`STALE_VERSION` / `WRITE_ON_MISSING` /
  `ALREADY_DELETED`
- `ErrNotFound`（删除从未提交过的主键，最后判定）

```go
if oe, ok := err.(*ontology.OpError); ok {
	switch oe.Code {
	case ontology.ErrInvalidArgument:
	case ontology.ErrVersionConflict:
		// oe.Reason 细分
	case ontology.ErrNotFound:
	}
}
```

## API 一览

| 方法 | 语义 |
| --- | --- |
| `NewStore(types, views)` | 按声明构建存储 |
| `Write(WriteRequest)` | 创建/更新；乐观凭证，返回新版本与全局序号 |
| `Delete(DeleteRequest)` | 墓碑化并原子扣除全部聚合贡献 |
| `Get(type,key)` | 读取当前存活实例 |
| `Query(view,group)` | `O(分组成员数)` 读取聚合值 |
| `Verify()` | 从源快照全量重算，校验增量索引是忠实派生物 |
| `Journal()` / `Replay(...)` | 已接受提交日志 / 确定性重放 |
