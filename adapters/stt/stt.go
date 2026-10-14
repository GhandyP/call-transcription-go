// Package stt defines the boundary between a recording and transcript text.
//
// Only a reference crosses that boundary. A Request carries a call id and an
// audio reference such as "s3://recordings/call-1.wav"; an implementation must
// never open, download, copy, store or return audio bytes, and Result carries
// no audio field at all. Every adapter states what it keeps through Retention,
// and the only policy this delivery supports is retaining nothing.
//
// An adapter returns segments exactly as the engine produced them. It does not
// sort them, does not rename speakers and does not invent findings: the
// transcript contract stays the single source of truth, so a defect the engine
// reported is still visible when the caller normalizes the call.
//
// This delivery ships one implementation, Fake, which is offline and
// deterministic. A vendor integration (Whisper, a cloud STT service, a
// diarization engine) is a separate slice with its own retention and access
// decisions, and it must satisfy this same interface.
package stt

import (
	"context"
	"errors"
	"strings"

	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

// Retention is what an adapter keeps from the recording it was pointed at.
type Retention int

const (
	// RetentionNone keeps nothing: the recording is never opened, downloaded,
	// copied or stored. It is the only supported policy.
	RetentionNone Retention = iota
)

// Errors returned by an adapter. Callers match them with errors.Is.
var (
	// ErrMissingCallID reports a request without a call identifier.
	ErrMissingCallID = errors.New("callID is required to transcribe a call")
	// ErrMissingAudioRef reports a request without a usable audio reference.
	ErrMissingAudioRef = errors.New("audioRef is required to transcribe a call")
)

// Request asks one transcriber to turn an audio reference into segments.
type Request struct {
	// CallID identifies the call the segments belong to.
	CallID string
	// AudioRef is a reference to the recording, never audio bytes.
	AudioRef string
	// Language is an optional BCP-47 hint. An empty value means unknown, and
	// an adapter must not guess one.
	Language string
}

// Validate reports whether the request carries everything an adapter needs
// before it does any work.
func (r Request) Validate() error {
	if strings.TrimSpace(r.CallID) == "" {
		return ErrMissingCallID
	}
	if strings.TrimSpace(r.AudioRef) == "" {
		return ErrMissingAudioRef
	}
	return nil
}

// Result is what an adapter produced for one call.
type Result struct {
	// Provider names the engine that produced the segments, so a caller can
	// report where the text came from instead of implying it came from a model.
	Provider string
	// Segments are the engine's segments, in the order the engine produced
	// them, with only the documented stable-id rule applied.
	Segments []transcript.Segment
}

// Transcriber turns one audio reference into transcript segments.
//
// An implementation must honour ctx cancellation and must keep the retention it
// reports: RetentionNone means the recording is never touched.
type Transcriber interface {
	Transcribe(ctx context.Context, req Request) (Result, error)
	// Retention reports what this adapter keeps from the recording.
	Retention() Retention
}
