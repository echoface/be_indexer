package be_indexer_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/segmentindex"
)

// 召回压测（segmentindex 引擎）：读取测试集 JSONL 文件构建索引（构建时间不计入耗时），
// 然后按多种查询形态执行召回 benchmark，另附 brute-force 语义校验。
//
// 数据文件每行一个 JSON 记录：{"id":..,"gender":..,"number_1..3":..,"char_1..5":..,"age_1..5":..}
// 默认数据集: ../static/testdata/bench_docs.json（可用 BE_BENCH_DOCS_FILE 覆盖）
//
// 语义说明（segmentindex.RetrieveAssignments）：查询未覆盖的字段跳过其约束，
// 全部字段都未覆盖时 conjunction 退化为通配命中；brute-force 模型按同一规则交叉校验。

const (
	recallDatasetEnv         = "BE_BENCH_DOCS_FILE"
	recallDatasetDefaultPath = "../static/testdata/bench_docs.json"
	recallQuerySeed          int64 = 20261106
	recallQueriesPerShape          = 256
	recallWarmupPerShape           = 32
	recallBrutePerShape            = 8
)

type (
	recallRecord struct {
		ID      int64  `json:"id"`
		Gender  int    `json:"gender"`
		Number1 int    `json:"number_1"`
		Number2 int    `json:"number_2"`
		Number3 int    `json:"number_3"`
		Char1   string `json:"char_1"`
		Char2   string `json:"char_2"`
		Char3   string `json:"char_3"`
		Char4   string `json:"char_4"`
		Char5   string `json:"char_5"`
		Age1    int    `json:"age_1"`
		Age2    int    `json:"age_2"`
		Age3    int    `json:"age_3"`
		Age4    int    `json:"age_4"`
		Age5    int    `json:"age_5"`
	}

	// recallFieldDef 数据记录字段 -> 索引字段的映射
	recallFieldDef struct {
		name be.BEField
		pick func(*recallRecord) interface{}
	}

	// recallExprSpec 文档模板中的一条布尔表达式
	recallExprSpec struct {
		fi    int  // recallFieldDefs 下标
		incl  bool // true: In, false: NotIn
		nVals int  // 取值个数
	}

	recallShape struct {
		name      string
		fields    []int // 查询覆盖的字段下标
		broad     bool  // 宽召回形态
		checkSelf bool  // 校验源文档一定命中
	}

	recallQuery struct {
		ass    be.Assignments
		selfID be.DocID // 生成该查询的源记录 id
	}

	recallShapeQueries struct {
		shape   recallShape
		queries []recallQuery
	}

	recallQueryPool struct {
		name    string
		queries []recallQuery
	}

	recallDataset struct {
		recs   []*recallRecord
		docs   []*be.Document
		shapes []recallShapeQueries
	}
)

var recallFieldDefs = []recallFieldDef{
	{"gender", func(r *recallRecord) interface{} { return r.Gender }},
	{"number_1", func(r *recallRecord) interface{} { return r.Number1 }},
	{"number_2", func(r *recallRecord) interface{} { return r.Number2 }},
	{"number_3", func(r *recallRecord) interface{} { return r.Number3 }},
	{"char_1", func(r *recallRecord) interface{} { return r.Char1 }},
	{"char_2", func(r *recallRecord) interface{} { return r.Char2 }},
	{"char_3", func(r *recallRecord) interface{} { return r.Char3 }},
	{"char_4", func(r *recallRecord) interface{} { return r.Char4 }},
	{"char_5", func(r *recallRecord) interface{} { return r.Char5 }},
	{"age_1", func(r *recallRecord) interface{} { return r.Age1 }},
	{"age_2", func(r *recallRecord) interface{} { return r.Age2 }},
	{"age_3", func(r *recallRecord) interface{} { return r.Age3 }},
	{"age_4", func(r *recallRecord) interface{} { return r.Age4 }},
	{"age_5", func(r *recallRecord) interface{} { return r.Age5 }},
}

const (
	recallFiGender = iota
	recallFiNum1
	recallFiNum2
	recallFiNum3
	recallFiChar1
	recallFiChar2
	recallFiChar3
	recallFiChar4
	recallFiChar5
	recallFiAge1
	recallFiAge2
	recallFiAge3
	recallFiAge4
	recallFiAge5
)

// recallFieldValues 取若干记录在指定字段上的值，按字段类型返回 []int / []string
func recallFieldValues(fi int, recs ...*recallRecord) be.Values {
	def := recallFieldDefs[fi]
	switch def.pick(recs[0]).(type) {
	case string:
		out := make([]string, 0, len(recs))
		for _, r := range recs {
			out = append(out, def.pick(r).(string))
		}
		return out
	default:
		out := make([]int, 0, len(recs))
		for _, r := range recs {
			out = append(out, def.pick(r).(int))
		}
		return out
	}
}

// recallTemplates 文档定向规则模板（按记录 id 轮转分配）
var recallTemplates = [][]recallExprSpec{
	{{recallFiGender, true, 1}},                                              // gender 定向
	{{recallFiAge1, true, 1}},                                                // age_1 定向
	{{recallFiGender, true, 1}, {recallFiAge1, true, 1}},                     // 人群基础定向
	{{recallFiGender, true, 1}, {recallFiAge1, true, 1}, {recallFiAge2, true, 1}, {recallFiAge3, true, 1}},
	{{recallFiAge1, true, 2}, {recallFiAge2, true, 2}},                       // 多值年龄段
	{{recallFiNum1, true, 1}},                                                // 高基数数值定向
	{{recallFiNum1, true, 4}, {recallFiAge1, true, 1}},                       // 数值多值
	{{recallFiChar1, true, 1}},                                               // 高基数字符串定向
	{{recallFiChar1, true, 1}, {recallFiChar2, true, 1}},                     // 字符串组合
	{{recallFiChar3, true, 8}, {recallFiChar4, true, 8}},                     // 字符串列表
	{{recallFiGender, true, 1}, {recallFiAge5, true, 1}, {recallFiNum2, true, 1}}, // 混合
	{{recallFiGender, true, 1}, {recallFiAge1, false, 3}},                    // 排除定向
	{{recallFiAge2, true, 1}, {recallFiGender, false, 1}, {recallFiAge3, false, 2}},
	{{recallFiGender, true, 1}, {recallFiAge1, true, 1}, {recallFiNum1, true, 1},
		{recallFiChar1, true, 1}, {recallFiNum2, true, 1}, {recallFiChar2, true, 1}}, // k=6
	{ // k=14 全字段
		{recallFiGender, true, 1}, {recallFiNum1, true, 1}, {recallFiNum2, true, 1}, {recallFiNum3, true, 1},
		{recallFiChar1, true, 1}, {recallFiChar2, true, 1}, {recallFiChar3, true, 1}, {recallFiChar4, true, 1},
		{recallFiChar5, true, 1}, {recallFiAge1, true, 1}, {recallFiAge2, true, 1}, {recallFiAge3, true, 1},
		{recallFiAge4, true, 1}, {recallFiAge5, true, 1},
	},
}

// recallShapes 查询形态：模拟不同宽窄的用户画像查询
var recallShapes = []recallShape{
	{"single_gender", []int{recallFiGender}, true, false},
	{"single_age1", []int{recallFiAge1}, true, false},
	{"demo4", []int{recallFiGender, recallFiAge1, recallFiAge2, recallFiAge3}, true, false},
	{"demo_char", []int{recallFiGender, recallFiAge1, recallFiChar1, recallFiChar2}, true, false},
	{"num1", []int{recallFiNum1}, false, false},
	{"char1", []int{recallFiChar1}, false, false},
	{"fine4", []int{recallFiNum1, recallFiNum2, recallFiChar1, recallFiChar2}, false, false},
	{"full14", []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}, false, true},
}

// recallPickValues 生成表达式取值：In 集合必含记录自身值（保证源文档命中），
// NotIn 集合绝不含自身值（保证源文档不被排除）
func recallPickValues(rng *rand.Rand, ds *recallDataset, fi int, self *recallRecord, n int, incl bool) be.Values {
	def := recallFieldDefs[fi]
	selfVal := def.pick(self)
	seen := make(map[interface{}]struct{}, n+1)
	raw := make([]interface{}, 0, n)
	if incl {
		raw = append(raw, selfVal)
		seen[selfVal] = struct{}{}
	}
	for tries := 0; len(raw) < n && tries < n*32; tries++ {
		v := def.pick(ds.recs[rng.Intn(len(ds.recs))])
		if _, dup := seen[v]; dup {
			continue
		}
		if !incl && v == selfVal {
			continue
		}
		seen[v] = struct{}{}
		raw = append(raw, v)
	}
	switch selfVal.(type) {
	case string:
		out := make([]string, 0, len(raw))
		for _, v := range raw {
			out = append(out, v.(string))
		}
		return out
	default:
		out := make([]int, 0, len(raw))
		for _, v := range raw {
			out = append(out, v.(int))
		}
		return out
	}
}

// recallBuildDocs 将记录转为 DNF 文档：每条记录一个 conjunction，模板按 id 轮转
func recallBuildDocs(ds *recallDataset, rng *rand.Rand) []*be.Document {
	docs := make([]*be.Document, 0, len(ds.recs))
	for _, rec := range ds.recs {
		tpl := recallTemplates[int(rec.ID)%len(recallTemplates)]
		conj := be.NewConjunction()
		for _, spec := range tpl {
			values := recallPickValues(rng, ds, spec.fi, rec, spec.nVals, spec.incl)
			field := recallFieldDefs[spec.fi].name
			if spec.incl {
				conj.In(field, values)
			} else {
				conj.NotIn(field, values)
			}
		}
		docs = append(docs, be.NewDocument(be.DocID(rec.ID)).AddConjunction(conj))
	}
	return docs
}

// recallLoadDataset 读取 JSONL 数据集并生成文档与查询
func recallLoadDataset(tb testing.TB) *recallDataset {
	tb.Helper()
	path := os.Getenv(recallDatasetEnv)
	if path == "" {
		path = recallDatasetDefaultPath
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && os.Getenv(recallDatasetEnv) == "" {
			tb.Skipf("recall dataset not found: %s (set %s to override)", path, recallDatasetEnv)
		}
		tb.Fatalf("open recall dataset %s: %v", path, err)
	}
	defer f.Close()

	ds := &recallDataset{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec recallRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			tb.Fatalf("decode recall record: %v", err)
		}
		ds.recs = append(ds.recs, &rec)
	}
	if err := scanner.Err(); err != nil {
		tb.Fatalf("read recall dataset: %v", err)
	}
	if len(ds.recs) == 0 {
		tb.Fatalf("recall dataset %s is empty", path)
	}

	rng := rand.New(rand.NewSource(recallQuerySeed))
	ds.docs = recallBuildDocs(ds, rng)

	// 每种形态生成固定数量查询，取值来自随机用户记录
	ds.shapes = make([]recallShapeQueries, 0, len(recallShapes))
	for _, shape := range recallShapes {
		queries := make([]recallQuery, 0, recallQueriesPerShape)
		for i := 0; i < recallQueriesPerShape; i++ {
			user := ds.recs[rng.Intn(len(ds.recs))]
			ass := make(be.Assignments, len(shape.fields))
			for _, fi := range shape.fields {
				ass[recallFieldDefs[fi].name] = recallFieldValues(fi, user)
			}
			queries = append(queries, recallQuery{ass: ass, selfID: be.DocID(user.ID)})
		}
		ds.shapes = append(ds.shapes, recallShapeQueries{shape: shape, queries: queries})
	}
	return ds
}

// recallBuildSegmentIndex 用 segmentindex 构建不可变索引：一个 conjunction 一个 segment group
func recallBuildSegmentIndex(tb testing.TB, ds *recallDataset) *segmentindex.Index {
	tb.Helper()
	builder := segmentindex.NewBuilder()
	if err := builder.AddDocuments(ds.docs...); err != nil {
		tb.Fatalf("add documents to segment index: %v", err)
	}
	index, err := builder.Seal()
	if err != nil {
		tb.Fatalf("seal segment index: %v", err)
	}
	return index
}

// ---------- brute-force 语义模型（交叉校验用） ----------

// recallValueHits 查询值集合与表达式值集合是否有交集
func recallValueHits(query, set be.Values) bool {
	switch q := query.(type) {
	case []int:
		s, ok := set.([]int)
		if !ok {
			return false
		}
		for _, sv := range s {
			for _, qv := range q {
				if sv == qv {
					return true
				}
			}
		}
	case []string:
		s, ok := set.([]string)
		if !ok {
			return false
		}
		for _, sv := range s {
			for _, qv := range q {
				if sv == qv {
					return true
				}
			}
		}
	}
	return false
}

// recallConjMatches conjunction 命中语义（segmentindex.RetrieveAssignments）：
// 查询未覆盖的字段跳过约束；覆盖的字段上：含 In 表达式则必须值有交集，
// NotIn 表达式与查询值有交集则排除；全部字段未覆盖时退化为通配命中
func recallConjMatches(conj *be.Conjunction, ass be.Assignments) bool {
	for field, exprs := range conj.Expressions {
		query, covered := ass[field]
		if !covered {
			continue
		}
		hasIncl, hitIncl, hitExcl := false, false, false
		for _, expr := range exprs {
			if expr.Incl {
				hasIncl = true
				if recallValueHits(query, expr.Value) {
					hitIncl = true
				}
			} else if recallValueHits(query, expr.Value) {
				hitExcl = true
			}
		}
		if hasIncl && !hitIncl {
			return false
		}
		if hitExcl {
			return false
		}
	}
	return true
}

// recallBruteForce 对全量文档暴力匹配（conj 之间为 OR）
func recallBruteForce(docs []*be.Document, ass be.Assignments) map[be.DocID]bool {
	matched := make(map[be.DocID]bool)
	for _, doc := range docs {
		for _, conj := range doc.Cons {
			if recallConjMatches(conj, ass) {
				matched[doc.ID] = true
				break
			}
		}
	}
	return matched
}

func recallCompareSets(got []be.DocID, want map[be.DocID]bool) error {
	if len(got) != len(want) {
		return fmt.Errorf("hit count mismatch: got %d want %d", len(got), len(want))
	}
	for _, id := range got {
		if !want[id] {
			return fmt.Errorf("unexpected doc id %d", id)
		}
	}
	return nil
}

// ---------- 共享构建（一次进程只构建一次，计时前完成） ----------

var (
	recallSetupOnce sync.Once
	recallSetupData *recallDataset
	recallSetupIdx  *segmentindex.Index
	recallSetupHits map[string]float64
)

// recallWarmup 预热并统计各形态平均命中数
func recallWarmup(tb testing.TB, ds *recallDataset, idx *segmentindex.Index) map[string]float64 {
	tb.Helper()
	hits := make(map[string]float64, len(ds.shapes))
	for _, sq := range ds.shapes {
		n := recallWarmupPerShape
		if n > len(sq.queries) {
			n = len(sq.queries)
		}
		total := 0
		for i := 0; i < n; i++ {
			q := sq.queries[i]
			got, err := idx.RetrieveAssignments(q.ass)
			if err != nil {
				tb.Fatalf("warmup shape %s query %d: %v", sq.shape.name, i, err)
			}
			total += len(got)
			if sq.shape.checkSelf && !be.DocIDList(got).Contain(q.selfID) {
				tb.Fatalf("warmup shape %s query %d: source doc %d not recalled", sq.shape.name, i, q.selfID)
			}
		}
		hits[sq.shape.name] = float64(total) / float64(n)
	}
	return hits
}

// recallSharedSetup 构建索引 + 预热，只执行一次；构建时间不计入 benchmark
func recallSharedSetup(tb testing.TB) (*recallDataset, *segmentindex.Index, map[string]float64) {
	tb.Helper()
	recallSetupOnce.Do(func() {
		ds := recallLoadDataset(tb)
		idx := recallBuildSegmentIndex(tb, ds)
		hits := recallWarmup(tb, ds, idx)
		recallSetupData, recallSetupIdx, recallSetupHits = ds, idx, hits
	})
	return recallSetupData, recallSetupIdx, recallSetupHits
}

// recallPools 并发压测查询池：mixed 全形态混合，broad 仅宽召回形态
func recallPools(ds *recallDataset) []recallQueryPool {
	mixed := make([]recallQuery, 0, len(ds.shapes)*recallQueriesPerShape)
	broad := make([]recallQuery, 0)
	for _, sq := range ds.shapes {
		mixed = append(mixed, sq.queries...)
		if sq.shape.broad {
			broad = append(broad, sq.queries...)
		}
	}
	rng := rand.New(rand.NewSource(recallQuerySeed + 7))
	rng.Shuffle(len(mixed), func(i, j int) { mixed[i], mixed[j] = mixed[j], mixed[i] })
	return []recallQueryPool{
		{"mixed", mixed},
		{"broad", broad},
	}
}

// ---------- 正确性测试：brute-force 交叉校验 ----------

func TestRecallDatasetPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping recall dataset test in short mode")
	}
	ds, idx, _ := recallSharedSetup(t)

	for _, sq := range ds.shapes {
		sq := sq
		t.Run(sq.shape.name, func(t *testing.T) {
			for i := 0; i < recallBrutePerShape; i++ {
				q := sq.queries[i%len(sq.queries)]
				want := recallBruteForce(ds.docs, q.ass)
				got, err := idx.RetrieveAssignments(q.ass)
				if err != nil {
					t.Fatalf("retrieve: %v", err)
				}
				if err := recallCompareSets(got, want); err != nil {
					t.Fatalf("query %d: %v assignments=%v", i, err, q.ass)
				}
			}
		})
	}
}

// ---------- 单线程召回 benchmark ----------

func BenchmarkRecallSegmentIndex(b *testing.B) {
	ds, idx, hits := recallSharedSetup(b)
	for _, sq := range ds.shapes {
		sq := sq
		b.Run(sq.shape.name, func(b *testing.B) {
			b.ReportMetric(float64(len(ds.docs)), "docs")
			b.ReportMetric(float64(len(idx.Segments())), "groups")
			b.ReportMetric(float64(len(sq.queries)), "queries")
			b.ReportMetric(hits[sq.shape.name], "avgHits")
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := idx.RetrieveAssignments(sq.queries[i%len(sq.queries)].ass); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ---------- 并发压测 benchmark ----------

func BenchmarkRecallSegmentIndexParallel(b *testing.B) {
	ds, idx, _ := recallSharedSetup(b)
	for _, pool := range recallPools(ds) {
		pool := pool
		b.Run(pool.name, func(b *testing.B) {
			b.ReportMetric(float64(len(ds.docs)), "docs")
			b.ReportMetric(float64(len(idx.Segments())), "groups")
			b.ReportMetric(float64(len(pool.queries)), "queries")
			b.ReportAllocs()
			b.ResetTimer()
			// 原子自增取起始偏移，避免所有 goroutine 锁步同一查询
			var next uint64
			b.RunParallel(func(pb *testing.PB) {
				start := int(atomic.AddUint64(&next, 1)) % len(pool.queries)
				i := 0
				for pb.Next() {
					q := pool.queries[(start+i)%len(pool.queries)]
					i++
					if _, err := idx.RetrieveAssignments(q.ass); err != nil {
						b.Error(err)
						return
					}
				}
			})
		})
	}
}

func Example_recallBenchmark() {
	fmt.Println("go test ./benchmark -run Recall -v")
	fmt.Println("go test ./benchmark -run '^$' -bench Recall -benchmem")
	// Output:
	// go test ./benchmark -run Recall -v
	// go test ./benchmark -run '^$' -bench Recall -benchmem
}
