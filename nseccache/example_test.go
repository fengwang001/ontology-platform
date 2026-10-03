package nseccache_test

import (
	"fmt"

	"ontology/nseccache"
)

func typesOf(codes ...uint16) map[uint16]bool {
	m := make(map[uint16]bool, len(codes))
	for _, c := range codes {
		m[c] = true
	}
	return m
}

// ExampleLookup 复现需求中给出的规范例子（zone=example, soaMin=200）。
func ExampleCache_Lookup() {
	c, err := nseccache.New("example", 200, 64)
	if err != nil {
		panic(err)
	}

	// R1: example -> d.example，Types={SOA(6),NS(2),NSEC(47)}，TTL=300，eff=min(300,200)=200
	r1 := nseccache.Record{
		Owner: "example", Next: "d.example",
		Types: typesOf(6, 2, 47), TTL: 300, Validated: true,
	}
	if err := c.Insert(1000, r1); err != nil {
		panic(err)
	}

	// R2: d.example -> example（回绕），Types={A(1),NSEC(47)}，TTL=100，eff=100
	r2 := nseccache.Record{
		Owner: "d.example", Next: "example",
		Types: typesOf(1, 47), TTL: 100, Validated: true,
	}
	if err := c.Insert(1100, r2); err != nil {
		panic(err)
	}

	show := func(now int, qname string, qtype uint16) {
		res, err := c.Lookup(now, qname, qtype)
		if err != nil {
			fmt.Printf("%s MX err=%v\n", qname, err)
			return
		}
		names := ""
		for i, r := range res.Used {
			if i > 0 {
				names += ","
			}
			names += r.Owner
		}
		fmt.Printf("%s t=%d -> kind=%s used=[%s] ttl=%d\n", qname, now, kindName(res.Kind), names, res.TTL)
	}

	show(1150, "b.example", 15)
	show(1150, "e.example", 15)
	show(1150, "z.d.example", 15)
	show(1150, "d.example", 15) // MX(15)：NoData
	show(1150, "d.example", 1)  // A：Miss
	show(1200, "b.example", 15) // 恰好全部过期：Miss

	// Output:
	// b.example t=1150 -> kind=NXDomain used=[example] ttl=50
	// e.example t=1150 -> kind=NXDomain used=[example,d.example] ttl=50
	// z.d.example t=1150 -> kind=NXDomain used=[d.example] ttl=50
	// d.example t=1150 -> kind=NoData used=[d.example] ttl=50
	// d.example t=1150 -> kind=Miss used=[] ttl=0
	// b.example t=1200 -> kind=Miss used=[] ttl=0
}

func kindName(k nseccache.ResultKind) string {
	switch k {
	case nseccache.ResultNXDomain:
		return "NXDomain"
	case nseccache.ResultNoData:
		return "NoData"
	default:
		return "Miss"
	}
}

// ExampleLookup_emptyNonTerminal 演示空非终结符的 NoData：
// 只有 c.example -> a.d.example（没有 d.example 自身记录），
// d.example 被覆盖且 k 等于其标签数。
func ExampleCache_Lookup_emptyNonTerminal() {
	c, _ := nseccache.New("example", 200, 64)
	rec := nseccache.Record{
		Owner: "c.example", Next: "a.d.example",
		Types: typesOf(47), TTL: 500, Validated: true,
	}
	if err := c.Insert(0, rec); err != nil {
		panic(err)
	}
	res, err := c.Lookup(0, "d.example", 1)
	if err != nil {
		panic(err)
	}
	fmt.Printf("d.example -> kind=%s used=[%s]\n", kindName(res.Kind), res.Used[0].Owner)
	// Output:
	// d.example -> kind=NoData used=[c.example]
}

func ExampleCache_Insert_errors() {
	_, err := nseccache.New("example", 200, 64)
	fmt.Println("new ok:", err == nil)

	c, _ := nseccache.New("example", 200, 64)

	// Types 不含 NSEC(47)：参数非法
	err = c.Insert(0, nseccache.Record{
		Owner: "a.example", Next: "b.example",
		Types: typesOf(1), TTL: 10, Validated: true,
	})
	fmt.Println("no nsec:", err == nseccache.ErrInvalidParam)

	// Validated=false：ErrNotValidated
	err = c.Insert(0, nseccache.Record{
		Owner: "a.example", Next: "b.example",
		Types: typesOf(47), TTL: 10, Validated: false,
	})
	fmt.Println("not validated:", err == nseccache.ErrNotValidated)

	// Owner 出区：ErrOutOfZone
	err = c.Insert(0, nseccache.Record{
		Owner: "a.other", Next: "b.example",
		Types: typesOf(47), TTL: 10, Validated: true,
	})
	fmt.Println("out of zone:", err == nseccache.ErrOutOfZone)

	// eff=0 也会清除同 Owner 旧记录
	_ = c.Insert(0, nseccache.Record{
		Owner: "a.example", Next: "b.example",
		Types: typesOf(47), TTL: 10, Validated: true,
	})
	_ = c.Insert(0, nseccache.Record{
		Owner: "a.example", Next: "b.example",
		Types: typesOf(47), TTL: 0, Validated: true,
	})
	res, _ := c.Lookup(1, "a.example", 1)
	fmt.Println("cleared by eff=0:", kindName(res.Kind) == "Miss")

	// 时钟回退
	err = c.Insert(-1, nseccache.Record{
		Owner: "a.example", Next: "b.example",
		Types: typesOf(47), TTL: 10, Validated: true,
	})
	fmt.Println("bad now:", err == nseccache.ErrInvalidParam)

	// Output:
	// new ok: true
	// no nsec: true
	// not validated: true
	// out of zone: true
	// cleared by eff=0: true
	// bad now: true
}
