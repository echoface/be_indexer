package segment

import (
	"encoding/binary"
	"math/rand"
	"sort"
	"testing"

	goahocorasick "github.com/anknown/ahocorasick"
)

// acHarness builds our DAT reader and remembers the ref->term mapping so tests
// can still assert on matched term strings (AC now returns posting refs, not
// terms). Each pattern is assigned PostingRef{Offset: index+1}.
type acHarness struct {
	r     *ACIndex
	byRef map[uint64]string
}

func buildOurAC(t testing.TB, patterns []string) *acHarness {
	t.Helper()
	b := NewACBuilder(BuilderEnv{})
	byRef := make(map[uint64]string, len(patterns))
	for i, p := range patterns {
		ref := PostingRef{Offset: uint64(i + 1), Count: 1}
		b.AddPosting(string(p), ref)
		byRef[ref.Offset] = p
	}
	bin, err := b.Compile()
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	r, err := NewACReader(bin)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return &acHarness{r: r, byRef: byRef}
}

// buildRefAC builds the reference anknown automaton (pure rune-level AC) used as
// a differential oracle.
func buildRefAC(t testing.TB, patterns []string) *goahocorasick.Machine {
	t.Helper()
	keywords := make([][]rune, 0, len(patterns))
	for _, p := range patterns {
		if p == "" {
			continue // reference rejects empty keywords
		}
		keywords = append(keywords, []rune(p))
	}
	m := new(goahocorasick.Machine)
	if err := m.Build(keywords); err != nil {
		t.Fatalf("ref build: %v", err)
	}
	return m
}

func refMatches(m *goahocorasick.Machine, text string) []string {
	terms := m.MultiPatternSearch([]rune(text), false)
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		out = append(out, string(term.Word))
	}
	sort.Strings(out)
	return out
}

func (h *acHarness) matches(text string) []string {
	refs := h.r.MatchPostingRefs(text)
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, h.byRef[ref.Offset])
	}
	sort.Strings(out)
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestACChinese(t *testing.T) {
	patterns := []string{"北京", "上海", "广告", "定向广告", "京东"}
	r := buildOurAC(t, patterns)
	ref := buildRefAC(t, patterns)
	texts := []string{
		"我在北京投放定向广告",
		"上海和北京都是大城市",
		"京东在北京",
		"广告主投放广告",
		"没有匹配项",
	}
	for _, txt := range texts {
		got := r.matches(txt)
		want := refMatches(ref, txt)
		if !eqStrings(got, want) {
			t.Fatalf("text=%q got=%v want=%v", txt, got, want)
		}
	}
}

func TestACEmojiAndMixed(t *testing.T) {
	patterns := []string{"😀", "👍🏻", "a😀b", "ab"}
	r := buildOurAC(t, patterns)
	ref := buildRefAC(t, patterns)
	for _, txt := range []string{"hello😀world", "a😀b👍🏻", "abc", "😀😀"} {
		got := r.matches(txt)
		want := refMatches(ref, txt)
		if !eqStrings(got, want) {
			t.Fatalf("text=%q got=%v want=%v", txt, got, want)
		}
	}
}

// TestACClassicFailLinks covers the textbook he/she/his/hers overlap where the
// fail links produce multiple suffix outputs at one position.
func TestACClassicFailLinks(t *testing.T) {
	patterns := []string{"he", "she", "his", "hers"}
	r := buildOurAC(t, patterns)
	ref := buildRefAC(t, patterns)
	for _, txt := range []string{"ushers", "his", "she", "hershey"} {
		got := r.matches(txt)
		want := refMatches(ref, txt)
		if !eqStrings(got, want) {
			t.Fatalf("text=%q got=%v want=%v", txt, got, want)
		}
	}
}

// TestACNestedSuffixOutputs forces multi-level suffix chains; every deeper
// state must aggregate the outputs of all of its suffix states.
func TestACNestedSuffixOutputs(t *testing.T) {
	patterns := []string{"a", "ba", "cba", "dcba", "xyz"}
	r := buildOurAC(t, patterns)
	for _, tt := range []struct {
		text string
		want []string
	}{
		{"a", []string{"a"}},
		{"ba", []string{"a", "ba"}},
		{"cba", []string{"a", "ba", "cba"}},
		{"dcba", []string{"a", "ba", "cba", "dcba"}},
		{"xdcbax", []string{"a", "ba", "cba", "dcba"}},
		{"xyz", []string{"xyz"}},
	} {
		got := r.matches(tt.text)
		sort.Strings(got)
		sort.Strings(tt.want)
		if !eqStrings(got, tt.want) {
			t.Fatalf("text=%q got=%v want=%v", tt.text, got, tt.want)
		}
	}
}

func TestACEmptyPattern(t *testing.T) {
	// Empty pattern must be ignored gracefully (no panic, no spurious match).
	r := buildOurAC(t, []string{"", "ab"})
	got := r.matches("xaby")
	if !eqStrings(got, []string{"ab"}) {
		t.Fatalf("got %v", got)
	}
}

// TestACDifferentialFuzz compares our DAT against the reference anknown machine
// over randomized Unicode (ASCII + CJK + emoji) pattern sets and texts.
func TestACDifferentialFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	alphabets := [][]rune{
		[]rune("abcde"),
		[]rune("北京上海广州深圳"),
		[]rune("😀👍🎯🔥"),
		[]rune("ab北😀"),
	}
	for iter := 0; iter < 300; iter++ {
		alpha := alphabets[rng.Intn(len(alphabets))]
		randStr := func(maxLen int) string {
			n := rng.Intn(maxLen) + 1
			rs := make([]rune, n)
			for i := range rs {
				rs[i] = alpha[rng.Intn(len(alpha))]
			}
			return string(rs)
		}
		// distinct patterns
		set := map[string]struct{}{}
		np := rng.Intn(8) + 1
		for i := 0; i < np; i++ {
			set[randStr(4)] = struct{}{}
		}
		patterns := make([]string, 0, len(set))
		for p := range set {
			patterns = append(patterns, p)
		}
		r := buildOurAC(t, patterns)
		ref := buildRefAC(t, patterns)
		for q := 0; q < 10; q++ {
			text := randStr(20)
			got := r.matches(text)
			want := refMatches(ref, text)
			if !eqStrings(got, want) {
				t.Fatalf("iter=%d patterns=%v text=%q\n got=%v\nwant=%v", iter, patterns, text, got, want)
			}
		}
	}
}

func TestACReaderTruncated(t *testing.T) {
	bin, _ := func() ([]byte, error) {
		b := NewACBuilder(BuilderEnv{})
		b.AddPosting("abc", PostingRef{Offset: 1, Count: 1})
		return b.Compile()
	}()
	if _, err := NewACReader(bin[:6]); err == nil {
		t.Fatal("expected truncated error")
	}
}

func corruptAC(t testing.TB, mutate func(bin []byte)) []byte {
	t.Helper()
	b := NewACBuilder(BuilderEnv{})
	if err := b.AddPosting("abc", PostingRef{Offset: 1, Count: 1}); err != nil {
		t.Fatal(err)
	}
	bin, err := b.Compile()
	if err != nil {
		t.Fatal(err)
	}
	bin = append([]byte(nil), bin...)
	mutate(bin)
	return bin
}

func TestACReaderRejectsCorruption(t *testing.T) {
	stateCount := func(bin []byte) uint32 {
		return binary.LittleEndian.Uint32(bin[0:4])
	}
	failAt := func(bin []byte, state uint32) int {
		symBytes := int(binary.LittleEndian.Uint32(bin[4:8])) * 4
		return 12 + symBytes + int(stateCount(bin))*4*2 + int(state)*4
	}

	tests := []struct {
		name   string
		mutate func(bin []byte)
	}{
		{"reserved", func(bin []byte) { binary.LittleEndian.PutUint32(bin[8:12], 1) }},
		{"symbol duplicate", func(bin []byte) { binary.LittleEndian.PutUint32(bin[12:16], binary.LittleEndian.Uint32(bin[16:20])) }},
		{"symbol invalid rune", func(bin []byte) { binary.LittleEndian.PutUint32(bin[12:16], 0xd800) }},
		{"fail out of range", func(bin []byte) {
			last := stateCount(bin) - 1
			binary.LittleEndian.PutUint32(bin[failAt(bin, last):failAt(bin, last)+4], stateCount(bin))
		}},
		{"fail cycle", func(bin []byte) {
			binary.LittleEndian.PutUint32(bin[failAt(bin, 1):failAt(bin, 1)+4], 2)
			binary.LittleEndian.PutUint32(bin[failAt(bin, 2):failAt(bin, 2)+4], 1)
		}},
		{"output truncated", func(bin []byte) {
			last := stateCount(bin) - 1
			outputAt := failAt(bin, last) + int(stateCount(bin))*4
			binary.LittleEndian.PutUint32(bin[outputAt:outputAt+4], 0xffffff)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin := corruptAC(t, tt.mutate)
			if _, err := NewACReader(bin); err == nil {
				t.Fatal("expected corruption to be rejected")
			}
		})
	}
}

func TestACMatchQueryPropagatesPostingRefError(t *testing.T) {
	b := NewACBuilder(BuilderEnv{})
	if err := b.AddPosting("abc", PostingRef{Offset: 1, Count: 100}); err != nil {
		t.Fatal(err)
	}
	bin, err := b.Compile()
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewACReader(bin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.MatchQuery(BlockContext{Pl: make([]byte, 8)}, "keyword", "abc"); err == nil {
		t.Fatal("expected out-of-range posting ref error")
	}
}

func BenchmarkACBuildChinese(b *testing.B) {
	// Large-ish Chinese dictionary to exercise alphabet compression.
	rng := rand.New(rand.NewSource(1))
	alpha := []rune("北京上海广州深圳杭州成都重庆武汉西安南京苏州天津")
	patterns := make([]string, 2000)
	for i := range patterns {
		n := rng.Intn(3) + 1
		rs := make([]rune, n)
		for j := range rs {
			rs[j] = alpha[rng.Intn(len(alpha))]
		}
		patterns[i] = string(rs)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bld := NewACBuilder(BuilderEnv{})
		for j, p := range patterns {
			bld.AddPosting(string(p), PostingRef{Offset: uint64(j + 1), Count: 1})
		}
		if _, err := bld.Compile(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkACSearchChinese(b *testing.B) {
	patterns := []string{"北京", "上海", "广告", "定向广告", "京东", "投放"}
	bld := NewACBuilder(BuilderEnv{})
	for j, p := range patterns {
		bld.AddPosting(string(p), PostingRef{Offset: uint64(j + 1), Count: 1})
	}
	bin, _ := bld.Compile()
	r, _ := NewACReader(bin)
	// MatchPostingRefs iterates the string directly (no []rune alloc) and returns
	// posting refs, so there is neither a rune-slice allocation nor a per-match
	// term string allocation.
	text := "我在北京和上海投放定向广告京东广告主反复投放投放"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.MatchPostingRefs(text)
	}
}
