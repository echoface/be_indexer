// Package segment provides a zero-copy implementation of segment storage.
// It writes and reads memory-mappable binary files for the Boolean Expression Index.
package segment

import "fmt"

// blockName builds the base block name for a (K, field) pair.
func blockName(k int, field string) string {
	return fmt.Sprintf("k%d_%s", k, field)
}

func dictBlockName(k int, field string) string  { return blockName(k, field) + "_dict" }
func plBlockName(k int, field string) string    { return blockName(k, field) + "_postings" }
func acBlockName(k int, field string) string    { return blockName(k, field) + "_ac" }
func rangeBlockName(k int, field string) string { return blockName(k, field) + "_range" }
