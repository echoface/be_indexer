package be_indexer

import (
	"github.com/echoface/be_indexer/builder"
	"github.com/echoface/be_indexer/engine"
	"github.com/echoface/be_indexer/loader"
	"github.com/echoface/be_indexer/manifest"
)

type (
	CompositeEngine         = engine.CompositeEngine
	IndexSnapshot           = engine.IndexSnapshot
	IndexHolder             = loader.Holder
	LoaderOptions           = loader.Options
	SegmentLoadMode         = loader.SegmentLoadMode
	Manifest                = manifest.Manifest
	SegmentDescriptor       = manifest.SegmentDescriptor
	FullIndexDescriptor     = manifest.FullIndexDescriptor
	DeltaIndexDescriptor    = manifest.DeltaIndexDescriptor
	SnapshotManifestRequest = builder.SnapshotManifestRequest
)

const (
	FormatVersionSegmentV5        = manifest.FormatVersionSegmentV5
	SegmentLoadHeapVerify         = loader.SegmentLoadHeapVerify
	SegmentLoadMmapVerify         = loader.SegmentLoadMmapVerify
	SegmentLoadMmapTrustPublished = loader.SegmentLoadMmapTrustPublished
)

var ErrManifestExists = manifest.ErrManifestExists

// NewCompositeEngine assembles an immutable serving snapshot. Use OpenIndex to
// load artifacts and apply latest-version visibility masks automatically.
func NewCompositeEngine(snapshot *IndexSnapshot) *CompositeEngine {
	return engine.NewCompositeEngine(snapshot)
}

// OpenIndex loads CURRENT into an owned full+delta engine. Close it after use.
func OpenIndex(root string, fields Schema, opts LoaderOptions) (*CompositeEngine, error) {
	return loader.OpenIndex(root, fields, opts)
}

// LoadSnapshot loads an owned snapshot without wrapping it in a CompositeEngine.
func LoadSnapshot(root string, fields Schema, opts LoaderOptions) (*IndexSnapshot, error) {
	return loader.LoadSnapshot(root, fields, opts)
}

// NewIndexHolder loads an index and manages concurrent queries and snapshot reloads.
func NewIndexHolder(root string, fields Schema, opts LoaderOptions) (*IndexHolder, error) {
	return loader.NewHolder(root, fields, opts)
}

// NewSnapshotManifest constructs and validates metadata for a full+delta generation.
func NewSnapshotManifest(req SnapshotManifestRequest) (Manifest, error) {
	return builder.NewSnapshotManifest(req)
}

// PublishManifest creates an immutable manifest and atomically updates CURRENT.
func PublishManifest(root, name string, m Manifest) error {
	return manifest.PublishManifest(root, name, m)
}

// PublishCurrent atomically selects an already published manifest reference.
func PublishCurrent(root, ref string) error { return manifest.PublishCurrent(root, ref) }

// WriteDocIDsSidecar writes a deterministic signed-ID sidecar and its checksum.
func WriteDocIDsSidecar(dir, name string, ids []DocID) (string, string, error) {
	return manifest.WriteDocIDsSidecar(dir, name, ids)
}
