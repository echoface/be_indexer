package segment

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/echoface/be_indexer/core"
	. "github.com/smartystreets/goconvey/convey"
)

type rejectedWriter struct{ err error }

func (w rejectedWriter) Write([]byte) (int, error) { return 0, w.err }

func TestSegmentBuilderDirectoryOwnership(t *testing.T) {
	Convey("builders sharing a parent own only their private run directories", t, func() {
		parent := t.TempDir()
		sentinel := filepath.Join(parent, "caller-owned.txt")
		So(os.WriteFile(sentinel, []byte("keep"), 0o600), ShouldBeNil)
		create := func(w io.Writer) *SegmentBuilder {
			b, err := NewSegmentBuilder(w, SegmentBuilderOptions{MaxDistinctKeysInMemory: 1, TmpDir: parent})
			So(err, ShouldBeNil)
			So(b.AddField("a", core.FieldOption{}), ShouldBeNil)
			So(b.AddRecord("a", "x", []core.EntryID{mkE(1, 1)}), ShouldBeNil)
			return b
		}
		first, second := create(new(bytes.Buffer)), create(new(bytes.Buffer))
		defer second.Close()
		firstDir, secondDir := first.tmpDir, second.tmpDir
		So(firstDir, ShouldNotEqual, secondDir)
		So(first.Write(), ShouldBeNil)
		_, err := os.Stat(firstDir)
		So(os.IsNotExist(err), ShouldBeTrue)
		_, err = os.Stat(secondDir)
		So(err, ShouldBeNil)
		So(first.AddRecord("a", "y", nil), ShouldEqual, ErrBuilderClosed)
		So(first.AddField("b", core.FieldOption{}), ShouldEqual, ErrBuilderClosed)
		So(first.Write(), ShouldEqual, ErrBuilderClosed)
		So(first.Close(), ShouldBeNil)
		So(second.Write(), ShouldBeNil)
		data, err := os.ReadFile(sentinel)
		So(err, ShouldBeNil)
		So(string(data), ShouldEqual, "keep")
		entries, err := os.ReadDir(parent)
		So(err, ShouldBeNil)
		So(len(entries), ShouldEqual, 1)

		Convey("write failure and abandonment also release only private resources", func() {
			cause := errors.New("writer rejected bytes")
			failed := create(rejectedWriter{cause})
			failedDir := failed.tmpDir
			So(errors.Is(failed.Write(), cause), ShouldBeTrue)
			_, err := os.Stat(failedDir)
			So(os.IsNotExist(err), ShouldBeTrue)
			abandoned := create(io.Discard)
			abandonedDir := abandoned.tmpDir
			So(abandoned.Close(), ShouldBeNil)
			So(abandoned.Close(), ShouldBeNil)
			_, err = os.Stat(abandonedDir)
			So(os.IsNotExist(err), ShouldBeTrue)
			_, err = os.Stat(sentinel)
			So(err, ShouldBeNil)
		})
	})
	Convey("memory-only builders never use the supplied temporary path", t, func() {
		path := filepath.Join(t.TempDir(), "must-not-be-created")
		b, err := NewSegmentBuilder(io.Discard, SegmentBuilderOptions{TmpDir: path})
		So(err, ShouldBeNil)
		So(b.AddField("a", core.FieldOption{}), ShouldBeNil)
		So(b.Write(), ShouldBeNil)
		_, err = os.Stat(path)
		So(os.IsNotExist(err), ShouldBeTrue)
		path = t.TempDir()
		b, err = NewSegmentBuilder(io.Discard, SegmentBuilderOptions{TmpDir: path})
		So(err, ShouldBeNil)
		So(b.Close(), ShouldBeNil)
		_, err = os.Stat(path)
		So(err, ShouldBeNil)
	})
}
