package cleanup

import (
	"errors"
	"testing"

	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

// TestCleanGuardRejectsABlankingRewrite proves the drift guard inside Clean is
// live: a rewrite step that empties a segment which had text is an error, not a
// silently accepted transcript.
func TestCleanGuardRejectsABlankingRewrite(t *testing.T) {
	in := transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
		{ID: "s1", Speaker: "Agent", Start: 0, End: 1, Text: "Hello there.", Confidence: 0.9},
	}}

	blanking := func(segment transcript.Segment, opts Options) (string, []transcript.Warning) {
		return "", nil
	}

	out, err := clean(in, DefaultOptions(), blanking)
	if !errors.Is(err, transcript.ErrBlankedText) {
		t.Fatalf("clean() error = %v, want %v", err, transcript.ErrBlankedText)
	}
	if len(out.Segments) != 0 {
		t.Errorf("clean() returned %d segments with an error, want none", len(out.Segments))
	}
}

// TestCleanGuardAcceptsAnIdentityRewrite proves the same guard accepts a rewrite
// that only touches text.
func TestCleanGuardAcceptsAnIdentityRewrite(t *testing.T) {
	in := transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
		{ID: "s1", Speaker: "Agent", Start: 0, End: 1, Text: "Hello there.", Confidence: 0.9},
	}}

	identity := func(segment transcript.Segment, opts Options) (string, []transcript.Warning) {
		return segment.Text, nil
	}

	out, err := clean(in, DefaultOptions(), identity)
	if err != nil {
		t.Fatalf("clean() error = %v, want nil", err)
	}
	if got, want := out.Segments[0].Text, "Hello there."; got != want {
		t.Errorf("Text = %q, want %q", got, want)
	}
}

func TestRedactionPlaceholdersAreStable(t *testing.T) {
	want := map[string]string{
		PlaceholderEmail:      "[redacted:email]",
		PlaceholderSSN:        "[redacted:ssn]",
		PlaceholderCard:       "[redacted:card]",
		PlaceholderAPIKey:     "[redacted:api_key]",
		PlaceholderToken:      "[redacted:token]",
		PlaceholderPrivateKey: "[redacted:private_key]",
		PlaceholderProfanity:  "[redacted:profanity]",
	}
	for got, expected := range want {
		if got != expected {
			t.Errorf("placeholder changed: got %q, want %q", got, expected)
		}
	}
}

func TestProfanityMatcherIgnoresInflections(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"that is shit", true},
		{"shitting on the plan", true},
		{"what a bastard", true},
		{"shitake mushrooms", false},
		{"a classic dessert", false},
		{"hello, is anyone there", false},
	}
	for _, tt := range tests {
		if got := matchesProfanity(tt.text); got != tt.want {
			t.Errorf("matchesProfanity(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}
