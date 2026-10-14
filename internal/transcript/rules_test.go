package transcript_test

import (
	"errors"
	"testing"

	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

func segmentIDs(segments []transcript.Segment) []string {
	ids := make([]string, 0, len(segments))
	for _, s := range segments {
		ids = append(ids, s.ID)
	}
	return ids
}

func codesFor(warnings []transcript.Warning, segmentID string) []transcript.WarningCode {
	codes := []transcript.WarningCode{}
	for _, w := range warnings {
		if w.SegmentID == segmentID {
			codes = append(codes, w.Code)
		}
	}
	return codes
}

func hasCode(codes []transcript.WarningCode, want transcript.WarningCode) bool {
	for _, c := range codes {
		if c == want {
			return true
		}
	}
	return false
}

// valid returns a minimal input that Normalize must accept unchanged.
func valid() transcript.CallInput {
	return transcript.CallInput{
		CallID: "call-1",
		Segments: []transcript.Segment{
			{ID: "s1", Speaker: "Customer", Start: 0, End: 6, Text: "I need to change my plan.", Confidence: 0.94},
			{ID: "s2", Speaker: "Agent", Start: 6, End: 20, Text: "Let me pull up your account.", Confidence: 0.9},
		},
	}
}

func TestNormalizeKeepsIdentitySpeakerAndRange(t *testing.T) {
	in := valid()
	out, err := transcript.Normalize(in, transcript.DefaultOptions())
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}

	if out.CallID != in.CallID {
		t.Errorf("CallID = %q, want %q", out.CallID, in.CallID)
	}
	if got, want := len(out.Segments), len(in.Segments); got != want {
		t.Fatalf("len(Segments) = %d, want %d", got, want)
	}
	for i, want := range in.Segments {
		got := out.Segments[i]
		if got.ID != want.ID {
			t.Errorf("segment %d ID = %q, want %q", i, got.ID, want.ID)
		}
		if got.Speaker != want.Speaker {
			t.Errorf("segment %d Speaker = %q, want %q", i, got.Speaker, want.Speaker)
		}
		if got.Start != want.Start || got.End != want.End {
			t.Errorf("segment %d range = [%v,%v], want [%v,%v]", i, got.Start, got.End, want.Start, want.End)
		}
		if got.Text != want.Text {
			t.Errorf("segment %d Text = %q, want %q", i, got.Text, want.Text)
		}
		if got.Confidence != want.Confidence {
			t.Errorf("segment %d Confidence = %v, want %v", i, got.Confidence, want.Confidence)
		}
	}
	if len(out.Warnings) != 0 {
		t.Errorf("Warnings = %+v, want none", out.Warnings)
	}
	if out.ReviewRequired {
		t.Error("ReviewRequired = true, want false for a clean transcript")
	}
}

func TestNormalizeSortsOutOfOrderTimestampsAndFlagsThem(t *testing.T) {
	in := loadFixture(t, "out_of_order.json")
	out, err := transcript.Normalize(in, transcript.DefaultOptions())
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}

	if got, want := segmentIDs(out.Segments), []string{"s1", "s2", "s3"}; !equalStrings(got, want) {
		t.Fatalf("segment order = %v, want %v", got, want)
	}
	for _, id := range []string{"s1", "s2", "s3"} {
		if !hasCode(codesFor(out.Warnings, id), transcript.CodeOutOfOrder) {
			t.Errorf("segment %s warnings = %v, want %s", id, codesFor(out.Warnings, id), transcript.CodeOutOfOrder)
		}
	}
	// Sorting must not move the ranges with the segments.
	want := map[string][2]float64{"s1": {0, 6}, "s2": {6, 20}, "s3": {20, 24}}
	for _, s := range out.Segments {
		if s.Start != want[s.ID][0] || s.End != want[s.ID][1] {
			t.Errorf("segment %s range = [%v,%v], want %v", s.ID, s.Start, s.End, want[s.ID])
		}
	}
}

func TestNormalizeFlagsOverlapOnBothSegments(t *testing.T) {
	in := loadFixture(t, "overlap.json")
	out, err := transcript.Normalize(in, transcript.DefaultOptions())
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}

	if got, want := segmentIDs(out.Segments), []string{"s1", "s2"}; !equalStrings(got, want) {
		t.Fatalf("segment order = %v, want %v", got, want)
	}
	for _, id := range []string{"s1", "s2"} {
		if !hasCode(codesFor(out.Warnings, id), transcript.CodeOverlap) {
			t.Errorf("segment %s warnings = %v, want %s", id, codesFor(out.Warnings, id), transcript.CodeOverlap)
		}
	}
}

func TestNormalizeFlagsGapBetweenSegments(t *testing.T) {
	in := loadFixture(t, "gap.json")
	out, err := transcript.Normalize(in, transcript.DefaultOptions())
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}

	if !hasCode(codesFor(out.Warnings, "s2"), transcript.CodeGap) {
		t.Errorf("segment s2 warnings = %v, want %s", codesFor(out.Warnings, "s2"), transcript.CodeGap)
	}
	if hasCode(codesFor(out.Warnings, "s1"), transcript.CodeGap) {
		t.Errorf("segment s1 warnings = %v, want no gap before the first segment", codesFor(out.Warnings, "s1"))
	}
}

func TestNormalizeFlagsUnknownSpeakerAndKeepsTheSegment(t *testing.T) {
	in := loadFixture(t, "unknown_speaker.json")
	out, err := transcript.Normalize(in, transcript.DefaultOptions())
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}

	if got, want := segmentIDs(out.Segments), []string{"s1", "s2", "s3"}; !equalStrings(got, want) {
		t.Fatalf("segment order = %v, want %v", got, want)
	}
	for _, s := range out.Segments {
		if s.ID == "s3" {
			if s.Speaker != "Agent" {
				t.Errorf("segment s3 Speaker = %q, want the known speaker untouched", s.Speaker)
			}
			if hasCode(codesFor(out.Warnings, "s3"), transcript.CodeUnknownSpeaker) {
				t.Error("segment s3 was flagged as an unknown speaker")
			}
			continue
		}
		if s.Speaker != transcript.UnknownSpeaker {
			t.Errorf("segment %s Speaker = %q, want %q", s.ID, s.Speaker, transcript.UnknownSpeaker)
		}
		if !hasCode(codesFor(out.Warnings, s.ID), transcript.CodeUnknownSpeaker) {
			t.Errorf("segment %s warnings = %v, want %s", s.ID, codesFor(out.Warnings, s.ID), transcript.CodeUnknownSpeaker)
		}
	}
}

func TestNormalizeFlagsLowConfidenceAndRequiresReview(t *testing.T) {
	in := loadFixture(t, "low_confidence.json")
	out, err := transcript.Normalize(in, transcript.DefaultOptions())
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}

	if !hasCode(codesFor(out.Warnings, "s1"), transcript.CodeLowConfidence) {
		t.Fatalf("segment s1 warnings = %v, want %s", codesFor(out.Warnings, "s1"), transcript.CodeLowConfidence)
	}
	if hasCode(codesFor(out.Warnings, "s2"), transcript.CodeLowConfidence) {
		t.Errorf("segment s2 warnings = %v, want no low confidence warning above the threshold", codesFor(out.Warnings, "s2"))
	}
	if !out.ReviewRequired {
		t.Error("ReviewRequired = false, want true for a low confidence call")
	}
	for _, w := range out.Warnings {
		if w.Code == transcript.CodeLowConfidence && !w.RequiresReview {
			t.Error("low confidence warning did not require review")
		}
	}
}

func TestNormalizeFlagsEmptyTranscriptForReview(t *testing.T) {
	out, err := transcript.Normalize(transcript.CallInput{CallID: "call-silent"}, transcript.DefaultOptions())
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}
	if len(out.Segments) != 0 {
		t.Errorf("len(Segments) = %d, want 0", len(out.Segments))
	}
	if !hasCode(codesFor(out.Warnings, ""), transcript.CodeEmptyTranscript) {
		t.Errorf("call warnings = %v, want %s", out.Warnings, transcript.CodeEmptyTranscript)
	}
	if !out.ReviewRequired {
		t.Error("ReviewRequired = false, want true when the transcript has no segments")
	}
}

func TestNormalizeRejectsInvalidInput(t *testing.T) {
	blank := ""
	tests := []struct {
		name string
		in   transcript.CallInput
		want error
	}{
		{
			name: "missing call id",
			in:   transcript.CallInput{Segments: valid().Segments},
			want: transcript.ErrMissingCallID,
		},
		{
			name: "blank segment id",
			in: transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
				{ID: "  ", Speaker: "Agent", Start: 0, End: 1, Text: "Hello.", Confidence: 0.9},
			}},
			want: transcript.ErrBlankSegmentID,
		},
		{
			name: "duplicate segment id",
			in: transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
				{ID: "s1", Speaker: "Agent", Start: 0, End: 1, Text: "Hello.", Confidence: 0.9},
				{ID: "s1", Speaker: "Customer", Start: 1, End: 2, Text: "Hi.", Confidence: 0.9},
			}},
			want: transcript.ErrDuplicateID,
		},
		{
			name: "negative start",
			in: transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
				{ID: "s1", Speaker: "Agent", Start: -1, End: 1, Text: "Hello.", Confidence: 0.9},
			}},
			want: transcript.ErrInvalidTimeRange,
		},
		{
			name: "end equals start",
			in: transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
				{ID: "s1", Speaker: "Agent", Start: 3, End: 3, Text: "Hello.", Confidence: 0.9},
			}},
			want: transcript.ErrInvalidTimeRange,
		},
		{
			name: "end before start",
			in: transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
				{ID: "s1", Speaker: "Agent", Start: 5, End: 4, Text: "Hello.", Confidence: 0.9},
			}},
			want: transcript.ErrInvalidTimeRange,
		},
		{
			name: "confidence above one",
			in: transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
				{ID: "s1", Speaker: "Agent", Start: 0, End: 1, Text: "Hello.", Confidence: 1.1},
			}},
			want: transcript.ErrInvalidConfidence,
		},
		{
			name: "confidence below zero",
			in: transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
				{ID: "s1", Speaker: "Agent", Start: 0, End: 1, Text: "Hello.", Confidence: -0.1},
			}},
			want: transcript.ErrInvalidConfidence,
		},
		{
			name: "present but blank audio reference",
			in:   transcript.CallInput{CallID: "call-1", AudioRef: &blank, Segments: valid().Segments},
			want: transcript.ErrInvalidAudioRef,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := transcript.Normalize(tt.in, transcript.DefaultOptions())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Normalize() error = %v, want %v", err, tt.want)
			}
			if len(out.Segments) != 0 {
				t.Errorf("Normalize() returned %d segments with an error, want none", len(out.Segments))
			}
		})
	}
}

func TestNormalizeIsDeterministic(t *testing.T) {
	in := loadFixture(t, "out_of_order.json")
	first, err := transcript.Normalize(in, transcript.DefaultOptions())
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}
	second, err := transcript.Normalize(in, transcript.DefaultOptions())
	if err != nil {
		t.Fatalf("Normalize() second error = %v, want nil", err)
	}
	if len(first.Warnings) != len(second.Warnings) {
		t.Fatalf("warning count changed between runs: %d then %d", len(first.Warnings), len(second.Warnings))
	}
	for i := range first.Warnings {
		if first.Warnings[i] != second.Warnings[i] {
			t.Fatalf("warning %d changed between runs: %+v then %+v", i, first.Warnings[i], second.Warnings[i])
		}
	}
	if in.Segments[0].ID != "s3" {
		t.Errorf("Normalize mutated the caller input: first segment is %q", in.Segments[0].ID)
	}
}

func TestValidateOutputAcceptsATextRewrite(t *testing.T) {
	in := valid()
	out, err := transcript.Normalize(in, transcript.DefaultOptions())
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}
	out.Segments[0].Text = "I need to change my plan."
	out.Segments[1].Text = "Let me pull up your account."

	if err := transcript.ValidateOutput(in, out); err != nil {
		t.Fatalf("ValidateOutput() error = %v, want nil for a text-only rewrite", err)
	}
}

func TestValidateOutputRejectsModelDrift(t *testing.T) {
	base := valid()
	normalized, err := transcript.Normalize(base, transcript.DefaultOptions())
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}

	tests := []struct {
		name   string
		mutate func(out *transcript.Transcript)
		want   error
	}{
		{
			name:   "dropped segment",
			mutate: func(out *transcript.Transcript) { out.Segments = out.Segments[:1] },
			want:   transcript.ErrOutputDrift,
		},
		{
			name: "invented segment",
			mutate: func(out *transcript.Transcript) {
				out.Segments = append(out.Segments, transcript.Segment{ID: "s9", Speaker: "Agent", Start: 20, End: 21, Text: "Anything else?", Confidence: 0.9})
			},
			want: transcript.ErrOutputDrift,
		},
		{
			name: "reordered segments",
			mutate: func(out *transcript.Transcript) {
				out.Segments[0], out.Segments[1] = out.Segments[1], out.Segments[0]
			},
			want: transcript.ErrOutputDrift,
		},
		{
			name:   "changed speaker",
			mutate: func(out *transcript.Transcript) { out.Segments[0].Speaker = "Agent" },
			want:   transcript.ErrOutputDrift,
		},
		{
			name:   "changed start",
			mutate: func(out *transcript.Transcript) { out.Segments[0].Start = 1 },
			want:   transcript.ErrOutputDrift,
		},
		{
			name:   "changed end",
			mutate: func(out *transcript.Transcript) { out.Segments[0].End = 7 },
			want:   transcript.ErrOutputDrift,
		},
		{
			name:   "changed confidence",
			mutate: func(out *transcript.Transcript) { out.Segments[0].Confidence = 0.5 },
			want:   transcript.ErrOutputDrift,
		},
		{
			name:   "changed call id",
			mutate: func(out *transcript.Transcript) { out.CallID = "call-2" },
			want:   transcript.ErrOutputDrift,
		},
		{
			name:   "blanked text",
			mutate: func(out *transcript.Transcript) { out.Segments[0].Text = "   " },
			want:   transcript.ErrBlankedText,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := normalized
			out.Segments = append([]transcript.Segment(nil), normalized.Segments...)
			tt.mutate(&out)

			if err := transcript.ValidateOutput(base, out); !errors.Is(err, tt.want) {
				t.Fatalf("ValidateOutput() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
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
