package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/GhandyP/call-transcription-go/internal/agent"
	"github.com/GhandyP/call-transcription-go/internal/cleanup"
	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

func TestPromptOmitsSensitiveCallID(t *testing.T) {
	const callID = "probe.person@example.com"
	prompt, err := agent.Prompt(transcript.Transcript{
		CallID:   callID,
		Segments: []transcript.Segment{{ID: "s1", Text: "Hello."}},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v, want nil", err)
	}
	if strings.Contains(prompt, callID) || strings.Contains(prompt, `"callID"`) {
		t.Errorf("Prompt() sent the local call ID to the model: %s", prompt)
	}
	for _, want := range []string{`"s1"`, "Hello."} {
		if !strings.Contains(prompt, want) {
			t.Errorf("Prompt() omitted source segment data %q: %s", want, prompt)
		}
	}
}

func TestCleanerKeepsSensitiveCallIDLocalAndSendsRedactedText(t *testing.T) {
	const callID = "probe.person@example.com"
	fake := &fakeModel{name: "fake-gemini", answer: `{"segments":[{"id":"s1","text":"Contact me at [redacted:email]."}]}`}
	cleaner, err := agent.NewCleaner(context.Background(), agent.Config{Model: fake})
	if err != nil {
		t.Fatalf("NewCleaner() error = %v, want nil", err)
	}

	out, err := cleaner.Clean(context.Background(), transcript.CallInput{
		CallID: callID,
		Segments: []transcript.Segment{{
			ID: "s1", Speaker: "Customer", Start: 0, End: 2,
			Text: "Contact me at " + callID + ".", Confidence: 0.9,
		}},
	})
	if err != nil {
		t.Fatalf("Clean() error = %v, want nil", err)
	}
	if fake.calls != 1 || len(fake.prompts) != 1 {
		t.Fatalf("model requests = %d, captured prompts = %d, want one each", fake.calls, len(fake.prompts))
	}
	if strings.Contains(fake.prompts[0], callID) || strings.Contains(fake.prompts[0], `"callID"`) {
		t.Errorf("model request included the local call ID: %s", fake.prompts[0])
	}
	if !strings.Contains(fake.prompts[0], cleanup.PlaceholderEmail) {
		t.Errorf("model request omitted redacted segment text: %s", fake.prompts[0])
	}
	if out.CallID != callID {
		t.Errorf("Clean() changed the external CallID to %q, want unchanged %q", out.CallID, callID)
	}
	if got, want := out.Segments[0].Text, "Contact me at "+cleanup.PlaceholderEmail+"."; got != want {
		t.Errorf("Clean() text = %q, want %q", got, want)
	}
}

func TestCleanerRejectsAnAnswerThatDropsRedactionPlaceholder(t *testing.T) {
	const email = "probe.person@example.com"
	fake := &fakeModel{name: "fake-gemini", answer: `{"segments":[{"id":"s1","text":"Contact details withheld."}]}`}
	cleaner, err := agent.NewCleaner(context.Background(), agent.Config{Model: fake})
	if err != nil {
		t.Fatalf("NewCleaner() error = %v, want nil", err)
	}

	out, err := cleaner.Clean(context.Background(), transcript.CallInput{
		CallID: "call-placeholder",
		Segments: []transcript.Segment{{
			ID: "s1", Speaker: "Customer", Start: 0, End: 2,
			Text: "Contact me at " + email + ".", Confidence: 0.9,
		}},
	})
	if !errors.Is(err, agent.ErrRedactionPlaceholderDrift) {
		t.Fatalf("Clean() error = %v, want %v", err, agent.ErrRedactionPlaceholderDrift)
	}
	if len(out.Segments) != 0 {
		t.Errorf("Clean() returned %d segments with an error, want no partial transcript", len(out.Segments))
	}
	if len(fake.prompts) != 1 || !strings.Contains(fake.prompts[0], cleanup.PlaceholderEmail) || strings.Contains(fake.prompts[0], email) {
		t.Errorf("model did not receive only the redacted text: %v", fake.prompts)
	}
}

func TestParseAnswerPreservesPlaceholderSequencesBySegmentID(t *testing.T) {
	reference := transcript.Transcript{
		CallID: "call-placeholder",
		Segments: []transcript.Segment{
			{ID: "s1", Speaker: "Customer", Start: 0, End: 5, Text: "My email " + cleanup.PlaceholderEmail + " and SSN " + cleanup.PlaceholderSSN, Confidence: 0.9},
			{ID: "s2", Speaker: "Agent", Start: 5, End: 10, Text: "Card " + cleanup.PlaceholderCard, Confidence: 0.8},
		},
		Warnings:       []transcript.Warning{transcript.NewWarning(transcript.CodeLowConfidence, "s2", "confidence below threshold")},
		ReviewRequired: true,
	}
	original := reference.Segments[0].Text
	raw := marshalPlaceholderAnswer(t,
		placeholderAnswerSegment{ID: "s2", Text: "The card is " + cleanup.PlaceholderCard + "."},
		placeholderAnswerSegment{ID: "s1", Text: "Please use " + cleanup.PlaceholderEmail + "; the SSN is " + cleanup.PlaceholderSSN + "."},
	)

	out, err := agent.ParseAnswer(reference, raw)
	if err != nil {
		t.Fatalf("ParseAnswer() error = %v, want nil", err)
	}
	if got, want := out.Segments[0].Text, "Please use "+cleanup.PlaceholderEmail+"; the SSN is "+cleanup.PlaceholderSSN+"."; got != want {
		t.Errorf("segment s1 text = %q, want %q", got, want)
	}
	if got, want := out.Segments[1].Text, "The card is "+cleanup.PlaceholderCard+"."; got != want {
		t.Errorf("segment s2 text = %q, want %q", got, want)
	}
	if out.Segments[0].ID != "s1" || out.Segments[0].Speaker != "Customer" || out.Segments[0].Start != 0 || out.Segments[0].End != 5 || out.Segments[0].Confidence != 0.9 {
		t.Errorf("ParseAnswer() changed s1 source facts: %+v", out.Segments[0])
	}
	if !out.ReviewRequired || len(out.Warnings) != 1 || out.Warnings[0].SegmentID != "s2" {
		t.Errorf("ParseAnswer() changed review findings: %+v", out.Warnings)
	}
	if reference.Segments[0].Text != original {
		t.Errorf("ParseAnswer() mutated source text to %q", reference.Segments[0].Text)
	}
}

func TestParseAnswerRejectsRedactionPlaceholderDrift(t *testing.T) {
	tests := []struct {
		name    string
		source1 string
		source2 string
		answer1 string
		answer2 string
	}{
		{
			name:    "removed",
			source1: "contact " + cleanup.PlaceholderEmail,
			source2: "payment " + cleanup.PlaceholderCard,
			answer1: "contact withheld",
			answer2: "payment " + cleanup.PlaceholderCard,
		},
		{
			name:    "altered",
			source1: "contact " + cleanup.PlaceholderEmail,
			source2: "payment " + cleanup.PlaceholderCard,
			answer1: "contact [redacted:phone]",
			answer2: "payment " + cleanup.PlaceholderCard,
		},
		{
			name:    "duplicated",
			source1: "contact " + cleanup.PlaceholderEmail,
			source2: "payment " + cleanup.PlaceholderCard,
			answer1: "contact " + cleanup.PlaceholderEmail + " " + cleanup.PlaceholderEmail,
			answer2: "payment " + cleanup.PlaceholderCard,
		},
		{
			name:    "newly inserted",
			source1: "plain contact",
			source2: "payment " + cleanup.PlaceholderCard,
			answer1: "plain " + cleanup.PlaceholderEmail,
			answer2: "payment " + cleanup.PlaceholderCard,
		},
		{
			name:    "reordered",
			source1: cleanup.PlaceholderEmail + " then " + cleanup.PlaceholderSSN,
			source2: "payment " + cleanup.PlaceholderCard,
			answer1: cleanup.PlaceholderSSN + " then " + cleanup.PlaceholderEmail,
			answer2: "payment " + cleanup.PlaceholderCard,
		},
		{
			name:    "moved across segments",
			source1: "contact " + cleanup.PlaceholderEmail,
			source2: "plain payment",
			answer1: "contact withheld",
			answer2: "plain payment " + cleanup.PlaceholderEmail,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reference := transcript.Transcript{CallID: "call-placeholder", Segments: []transcript.Segment{
				{ID: "s1", Speaker: "Customer", Start: 0, End: 5, Text: tt.source1, Confidence: 0.9},
				{ID: "s2", Speaker: "Agent", Start: 5, End: 10, Text: tt.source2, Confidence: 0.8},
			}}
			raw := marshalPlaceholderAnswer(t,
				placeholderAnswerSegment{ID: "s1", Text: tt.answer1},
				placeholderAnswerSegment{ID: "s2", Text: tt.answer2},
			)
			out, err := agent.ParseAnswer(reference, raw)
			if !errors.Is(err, agent.ErrRedactionPlaceholderDrift) {
				t.Fatalf("ParseAnswer() error = %v, want %v", err, agent.ErrRedactionPlaceholderDrift)
			}
			if out.CallID != "" || len(out.Segments) != 0 {
				t.Errorf("ParseAnswer() returned a partial transcript: %+v", out)
			}
			if reference.Segments[0].Text != tt.source1 || reference.Segments[1].Text != tt.source2 {
				t.Errorf("ParseAnswer() changed source reference text: %+v", reference.Segments)
			}
		})
	}
}

type placeholderAnswerSegment struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

func marshalPlaceholderAnswer(t *testing.T, segments ...placeholderAnswerSegment) []byte {
	t.Helper()
	raw, err := json.Marshal(struct {
		Segments []placeholderAnswerSegment `json:"segments"`
	}{Segments: segments})
	if err != nil {
		t.Fatalf("marshal answer: %v", err)
	}
	return raw
}
