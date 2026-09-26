package api

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"
)

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestAPIRoundTrip(t *testing.T) {
	c := New()
	lists := [][][]byte{
		{[]byte("hi"), {}, []byte("world!"), []byte("A")},
		{},
		{{}},
		{bytes.Repeat([]byte("z"), 5000), {}, []byte("end")},
	}
	for _, fs := range lists {
		got, err := c.UnmarshalFields(c.MarshalFields(fs))
		if err != nil || len(got) != len(fs) {
			t.Fatalf("UnmarshalFields = %d, %v", len(got), err)
		}
		for i := range fs {
			if !bytes.Equal(got[i], fs[i]) {
				t.Fatalf("field %d mismatch", i)
			}
		}
	}
}

func TestAPITruncatedRejected(t *testing.T) {
	c := New()
	rec := c.MarshalFields([][]byte{[]byte("hi"), []byte("world!")})
	if got, err := c.UnmarshalFields(rec[:len(rec)-1]); got != nil || err == nil {
		t.Fatalf("truncated: got %v, err %v", got, err)
	}
}

func TestConcurrent(t *testing.T) { // 并发 Encode/Decode/SelfCheck：无 sleep，WaitGroup 同步
	rng := rand.New(rand.NewSource(7))
	lists := make([][][]byte, 12)
	for i := range lists {
		fs := make([][]byte, 1+rng.Intn(6))
		for j := range fs {
			f := make([]byte, rng.Intn(200))
			rng.Read(f)
			fs[j] = f
		}
		lists[i] = fs
	}
	c := New()
	recs := make([][]byte, len(lists))
	for i, fs := range lists { // 串行参照
		recs[i] = c.MarshalFields(fs)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		for j := range lists {
			wg.Add(3)
			go func(j int) { // 并发解码同一段只读字节，逐字节相同
				defer wg.Done()
				got, err := c.UnmarshalFields(recs[j])
				if err != nil || len(got) != len(lists[j]) {
					t.Errorf("decode %d: %v", j, err)
					return
				}
				for k := range got {
					if !bytes.Equal(got[k], lists[j][k]) {
						t.Errorf("decode %d field %d mismatch", j, k)
					}
				}
			}(j)
			go func(j int) { // 并发编码与串行结果一致
				defer wg.Done()
				if !bytes.Equal(c.MarshalFields(lists[j]), recs[j]) {
					t.Errorf("encode %d differs", j)
				}
			}(j)
			go func() { // 并发自检
				defer wg.Done()
				if err := New().SelfCheck(); err != nil {
					t.Errorf("SelfCheck: %s", err)
				}
			}()
		}
	}
	wg.Wait()
}
