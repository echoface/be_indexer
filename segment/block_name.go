// Package segment provides a zero-copy implementation of segment storage.
// It writes and reads memory-mappable binary files for the Boolean Expression Index.
package segment

// blockName builds the base block name for a field.
func blockName(field string) string {
	return field
}

func dictBlockName(field string) string  { return blockName(field) + "_dict" }
func plBlockName(field string) string    { return blockName(field) + "_postings" }
func acBlockName(field string) string    { return blockName(field) + "_ac" }
func rangeBlockName(field string) string { return blockName(field) + "_range" }
