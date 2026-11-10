package be_indexer_test

import (
	"encoding/json"
	"testing"
	"time"

	be "github.com/echoface/be_indexer"
	"github.com/echoface/be_indexer/segment"
	. "github.com/smartystreets/goconvey/convey"
)

func TestRenamedAPISerialization(t *testing.T) {
	Convey("Existing document and flattened predicate JSON keep their format", t, func() {
		const documentJSON = `{"id":-7,"version":0,"cons":[{"predicates":{"age":[{"inc":true,"value":[18,35],"operator":3}],"city":[{"inc":true,"value":["bj","sh"]}]}}]}`
		doc := be.NewDocument(-7).AddConjunction(be.NewConjunction().Between("age", 18, 35).Include("city", []string{"bj", "sh"}))
		encoded, err := json.Marshal(doc)
		So(err, ShouldBeNil)
		So(string(encoded), ShouldEqual, documentJSON)

		var restored be.Document
		So(json.Unmarshal([]byte(documentJSON), &restored), ShouldBeNil)
		So(restored.Cons[0].Constraints["age"][0].Operator, ShouldEqual, be.ValueOpBetween)
		So(restored.Cons[0].FieldCount(), ShouldEqual, 2)
		So(restored.Cons[0].IncludeCount(), ShouldEqual, 2)
		encoded, err = json.Marshal(restored)
		So(err, ShouldBeNil)
		So(string(encoded), ShouldEqual, documentJSON)

		predicate := be.NewPredicateWithConstraint("age", be.NewConstraint(be.ValueOpLT, int64(55), false))
		encoded, err = json.Marshal(predicate)
		So(err, ShouldBeNil)
		So(string(encoded), ShouldEqual, `{"inc":false,"value":55,"operator":2,"field":"age"}`)
	})

	Convey("Existing maintenance JSON populates renamed fields and evaluates identically", t, func() {
		var opts be.IndexMaintenanceOptions
		So(json.Unmarshal([]byte(`{"Observation":{"FullAge":180000000000000}}`), &opts), ShouldBeNil)
		So(opts.Observation.TimeSinceFullBuild, ShouldEqual, 50*time.Hour)
		var stats be.IndexMaintenanceStats
		So(json.Unmarshal([]byte(`{"HasExactChangeCounts":true,"TombstoneRatio":0.5}`), &stats), ShouldBeNil)
		So(stats.ChangeCountsAvailable, ShouldBeTrue)
		So(stats.DeletedToChangedRatio, ShouldEqual, 0.5)
		recommendation := be.EvaluateIndexMaintenanceStats(stats, opts)
		So(recommendation.Decision, ShouldEqual, be.IndexMaintenanceDecisionRebuildFull)
		So(recommendation.Reasons, ShouldResemble, []be.IndexMaintenanceReason{
			{Code: "major_tombstone_ratio", Value: 0.5, Threshold: 0.2},
			{Code: "major_full_age", Value: float64(50 * time.Hour), Threshold: float64(48 * time.Hour)},
		})
		encoded, err := json.Marshal(opts.Observation)
		So(err, ShouldBeNil)
		So(string(encoded), ShouldEqual, `{"FullOnlyP99":0,"FullDeltaP99":0,"FullAge":180000000000000}`)
		encoded, err = json.Marshal(stats)
		So(err, ShouldBeNil)
		var fields map[string]json.RawMessage
		So(json.Unmarshal(encoded, &fields), ShouldBeNil)
		So(string(fields["TombstoneRatio"]), ShouldEqual, "0.5")
		So(string(fields["HasExactChangeCounts"]), ShouldEqual, "true")
		So(fields, ShouldNotContainKey, "DeletedToChangedRatio")
		So(fields, ShouldNotContainKey, "ChangeCountsAvailable")
		encoded, err = json.Marshal([]be.IndexMaintenanceDecision{
			be.IndexMaintenanceDecisionNone, be.IndexMaintenanceDecisionRebuildDelta, be.IndexMaintenanceDecisionRebuildFull,
		})
		So(err, ShouldBeNil)
		So(string(encoded), ShouldEqual, `["none","minor","major"]`)
	})

	Convey("Existing serialized build options and block contexts keep their keys", t, func() {
		for _, target := range []any{&be.BuildDirectoryOptions{}, &segment.SegmentBuilderOptions{}, &segment.BuilderEnv{}} {
			So(json.Unmarshal([]byte(`{"MaxPostingsInMemory":17}`), target), ShouldBeNil)
			encoded, err := json.Marshal(target)
			So(err, ShouldBeNil)
			var fields map[string]json.RawMessage
			So(json.Unmarshal(encoded, &fields), ShouldBeNil)
			So(string(fields["MaxPostingsInMemory"]), ShouldEqual, "17")
			So(fields, ShouldNotContainKey, "MaxDistinctKeysInMemory")
		}
		var ctx segment.BlockContext
		So(json.Unmarshal([]byte(`{"Pl":"AQID"}`), &ctx), ShouldBeNil)
		So(ctx.PostingBlock, ShouldResemble, []byte{1, 2, 3})
		encoded, err := json.Marshal(ctx)
		So(err, ShouldBeNil)
		So(string(encoded), ShouldEqual, `{"Pl":"AQID"}`)
	})
}
