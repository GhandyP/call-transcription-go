// Package transcript holds the call transcript contract and every rule that
// can be decided without a model.
//
// The pipeline is deliberately split: this package validates and normalizes a
// transcript deterministically, so the same input always produces the same
// output, the same warnings and the same exit code. Only a later package is
// allowed to talk to Gemini, and its answer is checked here before it is
// accepted: an answer that drops, invents, reorders or blanks a segment is
// rejected instead of repaired.
package transcript

import (
	"errors"
	"sort"
)

// UnknownSpeaker is the label written for a segment whose speaker could not be
// identified. The segment keeps its identity and range, and it carries a
// CodeUnknownSpeaker warning, so nothing is lost silently.
const UnknownSpeaker = "unknown"

// Segment is one spoken turn of a call transcript.
//
// ID, Speaker and the time range are source facts: they come from the STT step
// and survive the whole pipeline unchanged. Text is the only field a model is
// ever allowed to rewrite, which is what ValidateOutput enforces.
type Segment struct {
	ID         string  `json:"id"`
	Speaker    string  `json:"speaker"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// Duration returns the length of the segment in seconds.
func (s Segment) Duration() float64 { return s.End - s.Start }

// CallInput is the document the command reads: one call, an optional audio
// reference and the segments an upstream STT step produced.
//
// AudioRef is a reference (for example "s3://recordings/call-1.wav"), never
// audio bytes. This program never opens, downloads, copies or stores audio.
type CallInput struct {
	CallID   string    `json:"callID"`
	AudioRef *string   `json:"audioRef,omitempty"`
	Segments []Segment `json:"segments"`
}

// Transcript is the validated output document.
type Transcript struct {
	CallID   string    `json:"callID"`
	AudioRef *string   `json:"audioRef,omitempty"`
	Segments []Segment `json:"segments"`
	Warnings []Warning `json:"warnings"`

	// ReviewRequired is true when the transcript must not be used, published or
	// stored before a human reviews it. It is derived from the warnings, never
	// set by a model.
	ReviewRequired bool `json:"reviewRequired"`
}

// WarningCode enumerates the deterministic findings of this package. A finding
// describes what the caller still has to look at; it never changes a source
// fact by itself.
type WarningCode string

const (
	// CodeOutOfOrder reports a segment whose position in the input did not
	// match its start time. Normalize sorts the output by start time.
	CodeOutOfOrder WarningCode = "out_of_order_timestamp"
	// CodeOverlap reports two segments whose time ranges intersect.
	CodeOverlap WarningCode = "overlap"
	// CodeGap reports a silence between two consecutive segments that is
	// longer than Options.GapSeconds.
	CodeGap WarningCode = "gap"
	// CodeUnknownSpeaker reports a speaker label that could not be identified.
	CodeUnknownSpeaker WarningCode = "unknown_speaker"
	// CodeLowConfidence reports a segment below Options.LowConfidenceThreshold.
	CodeLowConfidence WarningCode = "low_confidence"
	// CodeEmptyTranscript reports a call with no segments at all.
	CodeEmptyTranscript WarningCode = "empty_transcript"
	// CodeEmptyText reports a segment that carries no text.
	CodeEmptyText WarningCode = "empty_text"
	// CodeRedacted reports text whose sensitive values were replaced.
	CodeRedacted WarningCode = "redacted"
	// CodeProfanity reports text that matched the configured profanity policy.
	CodeProfanity WarningCode = "profanity"
)

// RequiresHumanReview reports whether a finding gates the transcript on a human
// decision (S12). Unidentified speakers, overlaps and gaps are data-quality
// findings: they are visible in the output but do not gate it.
func (c WarningCode) RequiresHumanReview() bool {
	switch c {
	case CodeLowConfidence, CodeRedacted, CodeEmptyTranscript:
		return true
	default:
		return false
	}
}

// Warning is one finding about one segment, or about the call when SegmentID is
// empty.
type Warning struct {
	Code           WarningCode `json:"code"`
	SegmentID      string      `json:"segmentID,omitempty"`
	Detail         string      `json:"detail"`
	RequiresReview bool        `json:"requiresReview"`
}

// NewWarning builds a finding and stamps its review policy, so every package
// that reports findings applies the same gate. Detail must describe the rule
// that matched and never the value it matched.
func NewWarning(code WarningCode, segmentID, detail string) Warning {
	return Warning{
		Code:           code,
		SegmentID:      segmentID,
		Detail:         detail,
		RequiresReview: code.RequiresHumanReview(),
	}
}

// SortWarnings orders findings independently of how they were discovered:
// call-level findings first, then by segment, code and detail. Every caller
// that merges findings uses this, so the same input always yields the same
// output document.
func SortWarnings(warnings []Warning) {
	sort.SliceStable(warnings, func(i, j int) bool {
		a, b := warnings[i], warnings[j]
		if a.SegmentID != b.SegmentID {
			return a.SegmentID < b.SegmentID
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Detail < b.Detail
	})
}

// ReviewRequired reports whether any finding gates a transcript on a human.
func ReviewRequired(warnings []Warning) bool {
	for _, w := range warnings {
		if w.RequiresReview {
			return true
		}
	}
	return false
}

// Options are the thresholds of the deterministic rules. A value of zero or
// less selects the default from DefaultOptions.
type Options struct {
	// LowConfidenceThreshold is the lowest confidence that still passes
	// without a CodeLowConfidence warning.
	LowConfidenceThreshold float64
	// GapSeconds is the longest silence between consecutive segments that
	// passes without a CodeGap warning.
	GapSeconds float64
}

// DefaultOptions returns the documented thresholds.
func DefaultOptions() Options {
	return Options{LowConfidenceThreshold: 0.7, GapSeconds: 30}
}

// withDefaults replaces unset thresholds.
func (o Options) withDefaults() Options {
	if o.LowConfidenceThreshold <= 0 {
		o.LowConfidenceThreshold = DefaultOptions().LowConfidenceThreshold
	}
	if o.GapSeconds <= 0 {
		o.GapSeconds = DefaultOptions().GapSeconds
	}
	return o
}

// Errors returned by Normalize and ValidateOutput. Callers match them with
// errors.Is; the wrapped message names the offending segment.
var (
	// ErrMissingCallID reports an input without a call identifier.
	ErrMissingCallID = errors.New("callID is required")
	// ErrBlankSegmentID reports a segment without an identifier.
	ErrBlankSegmentID = errors.New("segment id is required")
	// ErrDuplicateID reports two segments sharing one identifier, which would
	// destroy segment identity.
	ErrDuplicateID = errors.New("duplicate segment id")
	// ErrInvalidTimeRange reports a range that is negative, inverted or not
	// finite. Such a segment cannot be repaired without inventing a fact.
	ErrInvalidTimeRange = errors.New("segment time range must be finite with end after start")
	// ErrInvalidConfidence reports a confidence outside [0,1].
	ErrInvalidConfidence = errors.New("segment confidence must be between 0 and 1")
	// ErrInvalidAudioRef reports a present but blank audio reference.
	ErrInvalidAudioRef = errors.New("audioRef must not be blank when present")
	// ErrOutputDrift reports an output whose segments no longer match the
	// input segments.
	ErrOutputDrift = errors.New("output segments do not match the input segments")
	// ErrBlankedText reports a segment that had text and came back blank.
	ErrBlankedText = errors.New("a segment that had text was blanked")
)
