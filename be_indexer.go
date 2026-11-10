// Package be_indexer builds and queries immutable Boolean Expression indexes.
// Documents are DNF rules. BuildSegment compiles them into Segment v5 bytes;
// NewEngine serves a fixed segment set, and NewIndexHolder loads atomically
// published full+delta snapshots with safe concurrent reloads.
//
// The root package is the application-facing API, organized by document, query,
// score, build, segment, snapshot and index maintenance assessment. EvaluateIndexMaintenance only
// returns advice; applications schedule delta merges and full rebuilds/publications.
// Production use recommends one full index plus one cumulative delta index.
// Advanced container implementations use the core, parser and segment packages directly.
//
// Posting data uses zero-copy views on aligned little-endian hosts. Complete
// queries may allocate for encoding, cursors and owned results. Reuse collectors
// to reduce allocation; collectors are local to one retrieval goroutine.
package be_indexer
