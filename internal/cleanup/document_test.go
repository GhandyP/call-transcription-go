package cleanup_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/GhandyP/call-transcription-go/internal/cleanup"
	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

// TestCleanKeepsTheDocumentArraysIsomorphicWithTheContract pins the JSON shape
// the case contract promises: segments and warnings are arrays, so a clean call
// must not emit null for either of them.
func TestCleanKeepsTheDocumentArraysIsomorphicWithTheContract(t *testing.T) {
	tests := []struct {
		name    string
		in      transcript.CallInput
		wantSub []string
	}{
		{
			name: "clean call",
			in: transcript.CallInput{CallID: "call-1", Segments: []transcript.Segment{
				{ID: "s1", Speaker: "Agent", Start: 0, End: 1, Text: "Hello.", Confidence: 0.9},
			}},
			wantSub: []string{`"warnings":[]`},
		},
		{
			name:    "call with no segments at all",
			in:      transcript.CallInput{CallID: "call-silent"},
			wantSub: []string{`"segments":[]`, `"warnings":[`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := cleanup.Clean(tt.in, cleanup.DefaultOptions())
			if err != nil {
				t.Fatalf("Clean() error = %v, want nil", err)
			}

			encoded, err := json.Marshal(out)
			if err != nil {
				t.Fatalf("marshal the transcript: %v", err)
			}
			for _, want := range tt.wantSub {
				if !strings.Contains(string(encoded), want) {
					t.Errorf("the document does not contain %s: %s", want, encoded)
				}
			}
			if strings.Contains(string(encoded), `":null`) {
				t.Errorf("the document carries a null array: %s", encoded)
			}
		})
	}
}
