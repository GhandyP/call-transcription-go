package stt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

// FakeProvider is the provider name the offline transcriber reports.
const FakeProvider = "fake-offline"

// Fake is an offline, deterministic Transcriber for tests, demos and CI.
//
// It never touches a recording: it answers with the segments it was built from,
// which makes the whole pipeline reproducible without credentials, without
// network access and without audio.
type Fake struct {
	segments []transcript.Segment
}

// NewFake builds a fake over the given segments. The slice is copied, so the
// caller keeps ownership of its own data.
func NewFake(segments []transcript.Segment) *Fake {
	return &Fake{segments: append([]transcript.Segment(nil), segments...)}
}

// NewFakeFromJSON builds a fake from a JSON array of segments, the shape of the
// fixtures under testdata.
func NewFakeFromJSON(data []byte) (*Fake, error) {
	var segments []transcript.Segment
	if err := json.Unmarshal(data, &segments); err != nil {
		return nil, fmt.Errorf("decode the offline STT fixture: %w", err)
	}
	return NewFake(segments), nil
}

// NewFakeFromFile builds a fake from a JSON fixture on disk. The file is a
// fixture, not a recording: the audio reference in a request is never opened.
func NewFakeFromFile(path string) (*Fake, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the offline STT fixture: %w", err)
	}
	return NewFakeFromJSON(data)
}

// Transcribe returns the fixture segments, or the context error with no result
// when the request has already been cancelled or its deadline has expired.
//
// A segment that arrived without an identifier gets one derived from its
// position in the fixture ("s1" for the first, "s2" for the second, and so on),
// so the same fixture always produces the same identifiers. A segment that
// already carries an identifier keeps it exactly. Nothing else is changed: the
// order, the speakers, the ranges, the text and the confidences are handed back
// as they arrived.
func (f *Fake) Transcribe(ctx context.Context, req Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := req.Validate(); err != nil {
		return Result{}, err
	}

	segments := make([]transcript.Segment, 0, len(f.segments))
	for i, segment := range f.segments {
		if strings.TrimSpace(segment.ID) == "" {
			segment.ID = fmt.Sprintf("s%d", i+1)
		}
		segments = append(segments, segment)
	}

	return Result{Provider: FakeProvider, Segments: segments}, nil
}

// Retention reports RetentionNone: the fake reads no audio, so there is nothing
// to keep.
func (f *Fake) Retention() Retention { return RetentionNone }
