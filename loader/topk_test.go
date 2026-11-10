package loader_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/echoface/be_indexer/core"
	"github.com/echoface/be_indexer/loader"
	. "github.com/smartystreets/goconvey/convey"
)

func TestHolderTopKScoreRefreshAndReload(t *testing.T) {
	Convey("score publication survives mmap index reload and explicit scores override it", t, func() {
		root := t.TempDir()
		writeSimpleIndex(t, root, "rank-1.json", 1, 1)
		writeSimpleIndex(t, root, "rank-2.json", 2, 2)
		current := filepath.Join(root, "CURRENT")
		So(os.WriteFile(current, []byte("rank-1.json"), 0600), ShouldBeNil)
		holder, err := loader.NewHolder(root, loaderFields(), loader.Options{})
		So(err, ShouldBeNil)
		defer holder.Close()
		source, err := core.NewBlockMaxScoreTable([]core.DocID{1}, []float64{99})
		So(err, ShouldBeNil)
		holder.SetScoreSource(source)
		result, err := holder.RetrieveTopK(core.Assignments{"a": 1}, 1, nil)
		So(err, ShouldBeNil)
		So(result, ShouldResemble, []core.ScoredDoc{{DocID: 1, Score: 99}})
		So(os.WriteFile(current, []byte("rank-2.json"), 0600), ShouldBeNil)
		So(holder.Reload(), ShouldBeNil)
		result, err = holder.RetrieveTopK(core.Assignments{"a": 2}, 1, nil)
		So(err, ShouldBeNil)
		So(result, ShouldResemble, []core.ScoredDoc{{DocID: 1, Score: 99}})
		override := core.NewDenseScoreTable(1, []float64{-2})
		result, err = holder.RetrieveTopK(core.Assignments{"a": 2}, 1, override)
		So(err, ShouldBeNil)
		So(result, ShouldResemble, []core.ScoredDoc{{DocID: 1, Score: -2}})
		holder.SetScoreSource(override)
		result, err = holder.RetrieveTopK(core.Assignments{"a": 2}, 1, nil)
		So(err, ShouldBeNil)
		So(result, ShouldResemble, []core.ScoredDoc{{DocID: 1, Score: -2}})
	})
}
