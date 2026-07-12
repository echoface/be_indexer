package segment

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/echoface/be_indexer/core"
)

func encodeEntriesBlock(entries core.Entries) []byte {
	buf := make([]byte, 16+len(entries)*8)
	copy(buf[:8], []byte("BEIENT1\x00"))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(len(entries)))
	for i, entry := range entries {
		binary.LittleEndian.PutUint64(buf[16+i*8:16+(i+1)*8], uint64(entry))
	}
	return buf
}

func decodeEntriesBlock(data []byte) (core.Entries, error) {
	if len(data) < 16 || string(data[:8]) != "BEIENT1\x00" {
		return nil, fmt.Errorf("invalid entries block")
	}
	count := binary.LittleEndian.Uint64(data[8:16])
	if count > uint64((len(data)-16)/8) || len(data) != 16+int(count)*8 {
		return nil, fmt.Errorf("truncated entries block")
	}
	if count == 0 {
		return nil, nil
	}
	if count <= math.MaxUint32 {
		return mapEntryIDs(data[16:16+int(count)*8], uint32(count)), nil
	}
	entries := make(core.Entries, count)
	for i := range entries {
		entries[i] = core.EntryID(binary.LittleEndian.Uint64(data[16+i*8 : 16+(i+1)*8]))
	}
	return entries, nil
}
