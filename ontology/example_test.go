package ontology_test

import (
	"fmt"

	"ontology/ontology"
)

// 演示：删除一列后新增同名列得到全新标识，旧版本事件按稳定标识解码，
// 空串与缺列得到区分。
func Example() {
	// 版本 1：a(#1), b(#2), c(#3)。关闭文件日志，避免污染示例输出。
	r, err := ontology.NewRegistry([]ontology.ColumnSpec{
		{Name: "a", Default: "DA"},
		{Name: "b", Default: "DB"},
		{Name: "c", Default: "DC"},
	}, ontology.WithLogger(discardLogger{}))
	if err != nil {
		panic(err)
	}

	// 版本 2：删除 b(#2)；版本 3：重新新增同名列 b，得到新标识 #4。
	if _, err := r.Evolve([]ontology.Change{{Kind: ontology.ChangeDrop, ID: 2}}); err != nil {
		panic(err)
	}
	if _, err := r.Evolve([]ontology.Change{{Kind: ontology.ChangeAdd, Name: "b", Default: "NEW_B"}}); err != nil {
		panic(err)
	}

	// 解码一条版本 1 的事件。旧 b 的值 "OLD_B" 属于已删除的 #2，
	// 不会按名字灌进新 b(#4)；新 b 取当前默认值 NEW_B。
	row, err := r.Decode(ontology.Event{
		Version: 1,
		Values:  []string{"1", "OLD_B", "3"},
	})
	if err != nil {
		panic(err)
	}
	for _, cv := range row.Ordered() {
		from := "event"
		if cv.Source == ontology.SourceDefault {
			from = "current-default"
		}
		fmt.Printf("#%d %s=%q from %s\n", cv.Column.ID, cv.Column.Name, cv.Value, from)
	}
	// Output:
	// #1 a="1" from event
	// #3 c="3" from event
	// #4 b="NEW_B" from current-default
}

type discardLogger struct{}

func (discardLogger) Logf(string, ...any) {}
