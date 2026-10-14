package agent

import (
	"encoding/json"
	"fmt"

	"google.golang.org/genai"

	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

const instruction = `You clean the text of ONE call transcript.

The segments you receive are already text from an upstream transcription step.
Never claim to have transcribed audio, never mention audio, recordings, Whisper
or speakers, and never add a segment that was not given to you.

Rules:
- Return exactly one entry per source segment, using the id you were given.
  Never change an id, never drop a segment, never invent one, never repeat one.
- Change the text only: fix spacing, punctuation and clearly mistyped words.
  Keep the language, the meaning and the tone of the speaker.
- Never summarise, never translate, never merge two segments, never split one.
- Some text carries a placeholder such as [redacted:email]. Keep every
  placeholder exactly as it is: never restore, guess, move or remove it.
- When a source segment has no text, return it with empty text.
- Answer with a single JSON object, and with nothing else.`

// Instruction returns the agent instruction. It pins the text-only boundary and
// the identity rule the parser enforces.
func Instruction() string { return instruction }

// OutputSchema describes the JSON object the model must return.
//
// Identity, speaker, time range and confidence are deliberately absent: the
// model is never asked for a source fact, so it cannot return one. Reducing the
// schema to id and text makes the drift the parser rejects structurally
// unlikely in the first place.
func OutputSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"segments": {
				Type:        genai.TypeArray,
				Description: "One entry per source segment, in any order.",
				Items: &genai.Schema{
					Type: genai.TypeObject,
					Properties: map[string]*genai.Schema{
						"id": {
							Type:        genai.TypeString,
							Description: "The id copied exactly from the source segment.",
						},
						"text": {
							Type:        genai.TypeString,
							Description: "The cleaned text of that segment. Empty when the source segment was empty.",
						},
					},
					Required: []string{"id", "text"},
				},
			},
		},
		Required: []string{"segments"},
	}
}

// promptDocument is what the model receives: identity and text, and nothing
// else. Speakers, timestamps, confidences, warnings and the audio reference
// never leave the process.
type promptDocument struct {
	CallID   string          `json:"callID"`
	Segments []promptSegment `json:"segments"`
}

type promptSegment struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Prompt renders the document the model is asked about.
func Prompt(t transcript.Transcript) (string, error) {
	document := promptDocument{
		CallID:   t.CallID,
		Segments: make([]promptSegment, 0, len(t.Segments)),
	}
	for _, segment := range t.Segments {
		document.Segments = append(document.Segments, promptSegment{ID: segment.ID, Text: segment.Text})
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode the agent prompt: %w", err)
	}
	return string(encoded), nil
}
