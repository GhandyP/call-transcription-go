package transcript_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

// loadFixture decodes one CallInput fixture from testdata.
func loadFixture(t *testing.T, name string) transcript.CallInput {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var in transcript.CallInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return in
}

func TestCallInputRoundTripKeepsEveryField(t *testing.T) {
	audioRef := "s3://recordings/call-1.wav"
	in := transcript.CallInput{
		CallID:   "call-1",
		AudioRef: &audioRef,
		Segments: []transcript.Segment{{
			ID:         "s1",
			Speaker:    "Customer",
			Start:      1.25,
			End:        6.5,
			Text:       "I need to change my plan.",
			Confidence: 0.94,
		}},
	}

	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal CallInput: %v", err)
	}
	var back transcript.CallInput
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal CallInput: %v", err)
	}

	if back.CallID != in.CallID {
		t.Errorf("CallID = %q, want %q", back.CallID, in.CallID)
	}
	if back.AudioRef == nil || *back.AudioRef != audioRef {
		t.Errorf("AudioRef = %v, want %q", back.AudioRef, audioRef)
	}
	if len(back.Segments) != 1 {
		t.Fatalf("len(Segments) = %d, want 1", len(back.Segments))
	}
	got := back.Segments[0]
	if got.ID != "s1" || got.Speaker != "Customer" {
		t.Errorf("identity changed: got id=%q speaker=%q", got.ID, got.Speaker)
	}
	if got.Start != 1.25 || got.End != 6.5 {
		t.Errorf("range changed: got start=%v end=%v, want 1.25/6.5", got.Start, got.End)
	}
	if got.Text != in.Segments[0].Text {
		t.Errorf("Text = %q, want %q", got.Text, in.Segments[0].Text)
	}
	if got.Confidence != 0.94 {
		t.Errorf("Confidence = %v, want 0.94", got.Confidence)
	}
}

func TestJSONOmitsAbsentAudioRef(t *testing.T) {
	raw, err := json.Marshal(transcript.CallInput{CallID: "call-1"})
	if err != nil {
		t.Fatalf("marshal CallInput: %v", err)
	}
	if got := string(raw); got != `{"callID":"call-1","segments":null}` {
		t.Errorf("marshal = %s, want callID with an absent audioRef", got)
	}
}

func TestTranscriptCarriesWarningsAndReviewFlag(t *testing.T) {
	out := transcript.Transcript{
		CallID:         "call-1",
		Segments:       []transcript.Segment{{ID: "s1", Speaker: "unknown", Start: 0, End: 3, Text: "Hello.", Confidence: 0.4}},
		Warnings:       []transcript.Warning{{Code: transcript.CodeLowConfidence, SegmentID: "s1", Detail: "confidence 0.4 is below 0.7", RequiresReview: true}},
		ReviewRequired: true,
	}

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal Transcript: %v", err)
	}
	var back transcript.Transcript
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal Transcript: %v", err)
	}
	if !back.ReviewRequired {
		t.Error("ReviewRequired = false, want true")
	}
	if len(back.Warnings) != 1 || back.Warnings[0].Code != transcript.CodeLowConfidence {
		t.Fatalf("Warnings = %+v, want one low_confidence warning", back.Warnings)
	}
	if !back.Warnings[0].RequiresReview {
		t.Error("Warning.RequiresReview = false, want true")
	}
	if back.Warnings[0].SegmentID != "s1" {
		t.Errorf("Warning.SegmentID = %q, want %q", back.Warnings[0].SegmentID, "s1")
	}
}

func TestWarningPolicyAndOrderingAreShared(t *testing.T) {
	lowConfidence := transcript.NewWarning(transcript.CodeLowConfidence, "s2", "confidence 0.40 is below the 0.70 threshold")
	if !lowConfidence.RequiresReview {
		t.Error("low_confidence did not require review")
	}
	overlap := transcript.NewWarning(transcript.CodeOverlap, "s2", "overlaps s1 by 0.500s")
	if overlap.RequiresReview {
		t.Error("overlap required review, want a data-quality finding that does not gate the transcript")
	}

	warnings := []transcript.Warning{
		overlap,
		transcript.NewWarning(transcript.CodeEmptyTranscript, "", "the call has no segments"),
		lowConfidence,
	}
	transcript.SortWarnings(warnings)

	want := []transcript.WarningCode{transcript.CodeEmptyTranscript, transcript.CodeLowConfidence, transcript.CodeOverlap}
	for i, code := range want {
		if warnings[i].Code != code {
			t.Fatalf("warnings[%d].Code = %q, want %q", i, warnings[i].Code, code)
		}
	}
	if !transcript.ReviewRequired(warnings) {
		t.Error("ReviewRequired = false, want true when a finding requires review")
	}
}
