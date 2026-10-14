package cleanup_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/GhandyP/call-transcription-go/internal/cleanup"
	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

// decodeFixture reads one CallInput fixture through the transcript contract.
func decodeFixture(t *testing.T, raw string) transcript.CallInput {
	t.Helper()
	var in transcript.CallInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return in
}

func clean(t *testing.T, in transcript.CallInput, opts cleanup.Options) transcript.Transcript {
	t.Helper()
	out, err := cleanup.Clean(in, opts)
	if err != nil {
		t.Fatalf("Clean() error = %v, want nil", err)
	}
	return out
}

func warningCodes(warnings []transcript.Warning, segmentID string) []transcript.WarningCode {
	codes := []transcript.WarningCode{}
	for _, w := range warnings {
		if w.SegmentID == segmentID {
			codes = append(codes, w.Code)
		}
	}
	return codes
}

func hasCode(warnings []transcript.Warning, segmentID string, want transcript.WarningCode) bool {
	for _, w := range warnings {
		if w.SegmentID == segmentID && w.Code == want {
			return true
		}
	}
	return false
}

func TestCleanRewritesOnlyTextAndKeepsEverySourceFact(t *testing.T) {
	in := transcript.CallInput{
		CallID: "call-1",
		Segments: []transcript.Segment{
			{ID: "s1", Speaker: "Customer", Start: 0, End: 4, Text: "  hello   ,,  world  !!!  ", Confidence: 0.94},
			{ID: "s2", Speaker: "Agent", Start: 4, End: 8, Text: "Sure.", Confidence: 0.9},
		},
	}

	out := clean(t, in, cleanup.DefaultOptions())

	if len(out.Segments) != len(in.Segments) {
		t.Fatalf("len(Segments) = %d, want %d", len(out.Segments), len(in.Segments))
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
		if got.Confidence != want.Confidence {
			t.Errorf("segment %d Confidence = %v, want %v", i, got.Confidence, want.Confidence)
		}
	}
	if got, want := out.Segments[0].Text, "hello, world!"; got != want {
		t.Errorf("segment s1 Text = %q, want %q", got, want)
	}
	if got, want := out.Segments[1].Text, "Sure."; got != want {
		t.Errorf("segment s2 Text = %q, want %q", got, want)
	}
	if out.ReviewRequired {
		t.Error("ReviewRequired = true, want false for a clean call")
	}
}

func TestCleanRedactsSensitiveValuesAndNeverLeaksThem(t *testing.T) {
	secrets := []string{
		"ana.perez@example.com",
		"123-45-6789",
		"4111 1111 1111 1111",
		"4111111111111111",
		"sk-live-9f8a7b6c5d4e3f2a1b0c",
		"AKIAIOSFODNN7EXAMPLE",
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		"MIIEowIBAAKCAQEA1234",
		"8f4b2c9d7e1a6f3b5c8d2e9a4f7b1c3d",
	}
	raw := `{"callID":"call-sensitive","segments":[
	 {"id":"s1","speaker":"Customer","start":0,"end":4,"text":"My email is ana.perez@example.com and my card is 4111 1111 1111 1111.","confidence":0.9},
	 {"id":"s2","speaker":"Agent","start":4,"end":8,"text":"I can see the SSN 123-45-6789 and the key sk-live-9f8a7b6c5d4e3f2a1b0c.","confidence":0.88},
	 {"id":"s3","speaker":"Customer","start":8,"end":12,"text":"Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9 and AKIAIOSFODNN7EXAMPLE.","confidence":0.85},
	 {"id":"s4","speaker":"Agent","start":12,"end":16,"text":"-----BEGIN RSA PRIVATE KEY----- MIIEowIBAAKCAQEA1234 -----END RSA PRIVATE KEY-----","confidence":0.8},
	 {"id":"s5","speaker":"Customer","start":16,"end":20,"text":"The booking reference is 4111111111111111 and the session token is 8f4b2c9d7e1a6f3b5c8d2e9a4f7b1c3d.","confidence":0.86}
	]}`
	in := decodeFixture(t, raw)

	out := clean(t, in, cleanup.DefaultOptions())

	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal transcript: %v", err)
	}
	for _, secret := range secrets {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the transcript still contains the raw value %q", secret)
		}
	}
	for _, placeholder := range []string{
		cleanup.PlaceholderEmail,
		cleanup.PlaceholderSSN,
		cleanup.PlaceholderCard,
		cleanup.PlaceholderAPIKey,
		cleanup.PlaceholderToken,
		cleanup.PlaceholderPrivateKey,
	} {
		if !strings.Contains(string(encoded), placeholder) {
			t.Errorf("the transcript never used %s", placeholder)
		}
	}
	for _, id := range []string{"s1", "s2", "s3", "s4", "s5"} {
		if !hasCode(out.Warnings, id, transcript.CodeRedacted) {
			t.Errorf("segment %s warnings = %v, want %s", id, warningCodes(out.Warnings, id), transcript.CodeRedacted)
		}
	}
	if !out.ReviewRequired {
		t.Error("ReviewRequired = false, want true when sensitive values were redacted")
	}
}

func TestCleanRedactionDetailsNameTheRuleAndNotTheValue(t *testing.T) {
	raw := `{"callID":"call-1","segments":[
	 {"id":"s1","speaker":"Agent","start":0,"end":4,"text":"Write to ana.perez@example.com please.","confidence":0.9}
	]}`
	in := decodeFixture(t, raw)

	out := clean(t, in, cleanup.DefaultOptions())

	if len(out.Warnings) != 1 {
		t.Fatalf("Warnings = %+v, want exactly one finding", out.Warnings)
	}
	detail := out.Warnings[0].Detail
	if !strings.Contains(detail, "email") {
		t.Errorf("warning detail = %q, want it to name the email rule", detail)
	}
	if strings.Contains(detail, "ana.perez@example.com") {
		t.Errorf("warning detail leaked the matched value: %q", detail)
	}
}

func TestCleanFlagsProfanityWithoutChangingTextByDefault(t *testing.T) {
	in := transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
		{ID: "s1", Speaker: "Customer", Start: 0, End: 4, Text: "This whole process is shit and nobody answers the phone.", Confidence: 0.9},
	}}

	out := clean(t, in, cleanup.DefaultOptions())

	if !hasCode(out.Warnings, "s1", transcript.CodeProfanity) {
		t.Errorf("segment s1 warnings = %v, want %s", warningCodes(out.Warnings, "s1"), transcript.CodeProfanity)
	}
	if !strings.Contains(out.Segments[0].Text, "shit") {
		t.Errorf("segment s1 Text = %q, want the words kept under the default flag-only policy", out.Segments[0].Text)
	}
	if out.ReviewRequired {
		t.Error("ReviewRequired = true, want false: profanity is flagged, not gated")
	}
}

func TestCleanRedactsProfanityWhenThePolicyAsksForIt(t *testing.T) {
	in := transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
		{ID: "s1", Speaker: "Customer", Start: 0, End: 4, Text: "This whole process is shit and nobody answers.", Confidence: 0.9},
	}}

	opts := cleanup.DefaultOptions()
	opts.Profanity = cleanup.ProfanityRedact

	out := clean(t, in, opts)

	if strings.Contains(out.Segments[0].Text, "shit") {
		t.Errorf("segment s1 Text = %q, want the matched word replaced", out.Segments[0].Text)
	}
	if !strings.Contains(out.Segments[0].Text, cleanup.PlaceholderProfanity) {
		t.Errorf("segment s1 Text = %q, want %s", out.Segments[0].Text, cleanup.PlaceholderProfanity)
	}
	if !hasCode(out.Warnings, "s1", transcript.CodeProfanity) {
		t.Errorf("segment s1 warnings = %v, want %s", warningCodes(out.Warnings, "s1"), transcript.CodeProfanity)
	}
}

func TestCleanDoesNotFlagWordsThatMerelyContainAProfaneTerm(t *testing.T) {
	in := transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
		{ID: "s1", Speaker: "Customer", Start: 0, End: 4, Text: "I will bring shitake mushrooms and a classic dessert.", Confidence: 0.9},
	}}

	out := clean(t, in, cleanup.DefaultOptions())

	if hasCode(out.Warnings, "s1", transcript.CodeProfanity) {
		t.Errorf("segment s1 warnings = %v, want no finding for a benign word", warningCodes(out.Warnings, "s1"))
	}
	if !strings.Contains(out.Segments[0].Text, "shitake") {
		t.Errorf("segment s1 Text = %q, want the benign word untouched", out.Segments[0].Text)
	}
}

func TestCleanIsDeterministic(t *testing.T) {
	raw := `{"callID":"call-1","segments":[
	 {"id":"s1","speaker":"","start":0,"end":4,"text":"Email me at a@b.co and see 123-45-6789.","confidence":0.4}
	]}`
	in := decodeFixture(t, raw)

	first, err := cleanup.Clean(in, cleanup.DefaultOptions())
	if err != nil {
		t.Fatalf("Clean() error = %v, want nil", err)
	}
	second, err := cleanup.Clean(in, cleanup.DefaultOptions())
	if err != nil {
		t.Fatalf("Clean() second error = %v, want nil", err)
	}

	a, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	b, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("two runs differ:\n%s\n%s", a, b)
	}
	if first.Segments[0].Speaker != transcript.UnknownSpeaker {
		t.Errorf("Speaker = %q, want %q", first.Segments[0].Speaker, transcript.UnknownSpeaker)
	}
	if !first.ReviewRequired {
		t.Error("ReviewRequired = false, want true for a low confidence call")
	}
}

func TestCleanDoesNotMutateTheCallerInput(t *testing.T) {
	raw := `{"callID":"call-1","segments":[
	 {"id":"s1","speaker":"","start":0,"end":4,"text":"Reach me at a@b.co  ","confidence":0.9}
	]}`
	in := decodeFixture(t, raw)
	original := in.Segments[0]

	if _, err := cleanup.Clean(in, cleanup.DefaultOptions()); err != nil {
		t.Fatalf("Clean() error = %v, want nil", err)
	}

	if in.Segments[0] != original {
		t.Errorf("the caller input changed: %+v, want %+v", in.Segments[0], original)
	}
}

func TestCleanPropagatesInputErrors(t *testing.T) {
	tests := []struct {
		name string
		in   transcript.CallInput
		want error
	}{
		{
			name: "missing call id",
			in:   transcript.CallInput{Segments: []transcript.Segment{{ID: "s1", Speaker: "Agent", Start: 0, End: 1, Text: "Hi.", Confidence: 0.9}}},
			want: transcript.ErrMissingCallID,
		},
		{
			name: "duplicate segment id",
			in: transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
				{ID: "s1", Speaker: "Agent", Start: 0, End: 1, Text: "Hi.", Confidence: 0.9},
				{ID: "s1", Speaker: "Agent", Start: 1, End: 2, Text: "Hi again.", Confidence: 0.9},
			}},
			want: transcript.ErrDuplicateID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := cleanup.Clean(tt.in, cleanup.DefaultOptions())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Clean() error = %v, want %v", err, tt.want)
			}
			if len(out.Segments) != 0 {
				t.Errorf("Clean() returned %d segments with an error, want none", len(out.Segments))
			}
		})
	}
}
