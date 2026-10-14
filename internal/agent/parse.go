package agent

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

// Errors returned by ParseAnswer. Callers match them with errors.Is.
var (
	// ErrMalformedAnswer reports an answer that is not the JSON object the
	// output schema asks for.
	ErrMalformedAnswer = errors.New("the agent answer is not the JSON object the schema asks for")
	// ErrMissingSegmentText reports a source segment the answer does not cover.
	ErrMissingSegmentText = errors.New("the agent answer does not cover every source segment")
	// ErrUnknownSegmentID reports an answer entry whose id is not a source
	// segment.
	ErrUnknownSegmentID = errors.New("the agent answer carries an id that is not a source segment")
	// ErrDuplicateSegmentText reports a source segment the answer returned more
	// than once.
	ErrDuplicateSegmentText = errors.New("the agent answer repeats a source segment")
)

// answerDocument is the shape OutputSchema asks for.
type answerDocument struct {
	Segments []answerSegment `json:"segments"`
}

type answerSegment struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// ParseAnswer turns a model answer into a validated transcript.
//
// The answer may reorder its entries, because identity is what matters: the
// result is built from the reference segments, never from the order the model
// chose. Text is the only field the answer can influence. Everything else is
// checked with transcript.ValidateOutput, so an answer that drops, invents,
// repeats or blanks a segment is rejected instead of repaired.
func ParseAnswer(reference transcript.Transcript, raw []byte) (transcript.Transcript, error) {
	if len(reference.Segments) == 0 {
		return reference, nil
	}

	var parsed answerDocument
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return transcript.Transcript{}, fmt.Errorf("%w: %v", ErrMalformedAnswer, err)
	}
	if len(parsed.Segments) == 0 {
		return transcript.Transcript{}, fmt.Errorf("%w: it lists no segments", ErrMalformedAnswer)
	}

	texts := make(map[string]string, len(parsed.Segments))
	for _, entry := range parsed.Segments {
		if _, duplicate := texts[entry.ID]; duplicate {
			return transcript.Transcript{}, fmt.Errorf("%w: segment %q appears twice", ErrDuplicateSegmentText, entry.ID)
		}
		texts[entry.ID] = entry.Text
	}
	for id := range texts {
		if !hasSegment(reference, id) {
			return transcript.Transcript{}, fmt.Errorf("%w: %q", ErrUnknownSegmentID, id)
		}
	}

	out := reference
	out.Segments = make([]transcript.Segment, len(reference.Segments))
	copy(out.Segments, reference.Segments)
	for i, source := range reference.Segments {
		text, ok := texts[source.ID]
		if !ok {
			return transcript.Transcript{}, fmt.Errorf("%w: no text for segment %q", ErrMissingSegmentText, source.ID)
		}
		out.Segments[i].Text = text
	}

	if err := transcript.ValidateOutput(referenceInput(reference), out); err != nil {
		return transcript.Transcript{}, err
	}
	return out, nil
}

// hasSegment reports whether the reference carries one segment with that id.
func hasSegment(reference transcript.Transcript, id string) bool {
	for _, segment := range reference.Segments {
		if segment.ID == id {
			return true
		}
	}
	return false
}

// referenceInput rebuilds the input document a reference transcript came from,
// which is what the drift guard compares against.
func referenceInput(reference transcript.Transcript) transcript.CallInput {
	return transcript.CallInput{
		CallID:   reference.CallID,
		AudioRef: reference.AudioRef,
		Segments: reference.Segments,
	}
}
