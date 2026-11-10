package be_indexer_test

import (
	"bytes"
	"fmt"
	"os"

	be "github.com/echoface/be_indexer"
)

func ExampleNewConstraint() {
	constraint := be.NewConstraint(be.ValueOpGT, int64(18), true)
	predicate := be.NewPredicateWithConstraint("age", constraint)
	conjunction := be.NewConjunction().
		Include("city", []string{"bj", "sh"}).
		Exclude("city", "gz").
		AddPredicates(predicate).
		Exclude("device", "bot")
	fmt.Println(conjunction.FieldCount(), conjunction.IncludeCount())
	// Output: 3 2
}

func ExampleBuildSegment() {
	fields := be.Schema{
		"age":  {IndexType: be.IndexNameExtendRange, Encoder: "ext_range"},
		"city": {IndexType: be.IndexNameDefault},
	}
	docs := []*be.Document{
		be.NewDocument(1).AddConjunction(be.NewConjunction().Between("age", 18, 25).Include("city", "beijing")),
		be.NewDocument(2).AddConjunction(be.NewConjunction().Exclude("city", "rural")),
	}
	var buf bytes.Buffer
	if err := be.BuildSegment(&buf, fields, docs, be.BuildSegmentOptions{}); err != nil {
		panic(err)
	}
	reader, err := be.NewSegmentReaderWithOptions(buf.Bytes(), be.SegmentReaderOptions{
		BlockChecksumMode: be.BlockChecksumOnOpen,
	})
	if err != nil {
		panic(err)
	}
	eng, err := be.NewEngine(fields, []*be.SegmentReader{reader})
	if err != nil {
		_ = reader.Close()
		panic(err)
	}
	defer eng.Close()
	query := be.Assignments{"age": 20, "city": "beijing"}
	collector := be.NewDocIDCollector() // This worker owns and reuses it.
	collector.Reset()
	if err := eng.RetrieveWithCollector(query, collector, be.WithStrictQuery()); err != nil {
		panic(err)
	}
	owned := collector.Bitmap().Clone()
	collector.Reset()
	fmt.Println(owned.Contains(1), owned.Contains(2), collector.DocCount())

	// K=0 still obeys its Exclude constraint.
	if err := eng.RetrieveWithCollector(be.Assignments{"city": "rural"}, collector); err != nil {
		panic(err)
	}
	fmt.Println(collector.DocCount())
	// Output:
	// true true 0
	// 0
}

func ExampleNewIndexHolder() {
	root, err := os.MkdirTemp("", "be-indexer-example-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(root)
	fields := be.Schema{"city": {IndexType: be.IndexNameDefault}}
	full, err := be.NewFullIndexBuilder(be.FullIndexBuildOptions{
		Root: root, Generation: 1, SnapshotWatermark: 10, Fields: fields,
	})
	if err != nil {
		panic(err)
	}
	defer full.Close()
	if err := full.AddDocument(be.NewDocument(1).AddConjunction(be.NewConjunction().Include("city", "bj"))); err != nil {
		panic(err)
	}
	fullDesc, err := full.Build()
	if err != nil {
		panic(err)
	}
	delta, err := be.NewDeltaIndexBuilder(be.DeltaIndexBuildOptions{
		Root: root, Generation: 2, FromWatermarkExclusive: 10, ToWatermarkInclusive: 11, Fields: fields,
	})
	if err != nil {
		panic(err)
	}
	defer delta.Close()
	if err := delta.AddMutation(be.Mutation{DocID: 1, Version: 1, Op: be.MutationDelete}); err != nil {
		panic(err)
	}
	if err := delta.AddMutation(be.Mutation{
		DocID: 2, Version: 1, Op: be.MutationUpsert,
		Document: be.NewDocument(2).AddConjunction(be.NewConjunction().Include("city", "bj")),
	}); err != nil {
		panic(err)
	}
	deltaDesc, err := delta.Build()
	if err != nil {
		panic(err)
	}
	m, err := be.NewSnapshotManifest(be.SnapshotManifestRequest{
		IndexName: "example", Generation: 2, Fields: fields,
		Full: fullDesc, Deltas: []be.DeltaIndexDescriptor{deltaDesc},
	})
	if err != nil {
		panic(err)
	}
	if err := be.PublishManifest(root, "manifest-2.json", m); err != nil {
		panic(err)
	}
	holder, err := be.NewIndexHolder(root, fields, be.LoaderOptions{SegmentLoad: be.SegmentLoadMmapVerify})
	if err != nil {
		panic(err)
	}
	defer holder.Close()
	result, err := holder.Retrieve(be.Assignments{"city": "bj"})
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Contains(1), result.Contains(2))
	// Output: false true
}
