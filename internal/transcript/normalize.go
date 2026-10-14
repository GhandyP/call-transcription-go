package transcript

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Normalize validates every source fact, orders the segments by start time and
// collects the deterministic findings.
//
// It either returns a transcript whose segments keep the identity, speaker and
// range they arrived with, or it returns an error and no transcript at all.
// Findings never silence a fact: an unidentified speaker becomes
// UnknownSpeaker and is flagged, an out-of-order segment is moved to its place
// in time and is flagged.
//
// The caller's input is never mutated.
func Normalize(in CallInput, opts Options) (Transcript, error) {
	opts = opts.withDefaults()

	if strings.TrimSpace(in.CallID) == "" {
		return Transcript{}, ErrMissingCallID
	}
	if in.AudioRef != nil && strings.TrimSpace(*in.AudioRef) == "" {
		return Transcript{}, ErrInvalidAudioRef
	}
	if err := validateSegments(in.Segments); err != nil {
		return Transcript{}, err
	}

	// Work on a copy so the caller's document is never touched.
	type placed struct {
		segment Segment
		origin  int
	}
	ordered := make([]placed, 0, len(in.Segments))
	for i, s := range in.Segments {
		ordered = append(ordered, placed{segment: s, origin: i})
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].segment.Start != ordered[j].segment.Start {
			return ordered[i].segment.Start < ordered[j].segment.Start
		}
		return ordered[i].segment.ID < ordered[j].segment.ID
	})

	segments := make([]Segment, 0, len(ordered))
	warnings := make([]Warning, 0, len(in.Segments))

	for position, item := range ordered {
		s := item.segment

		if IsUnknownSpeaker(s.Speaker) {
			warnings = append(warnings, newWarning(CodeUnknownSpeaker, s.ID, fmt.Sprintf(
				"speaker label %q was not identified and was written as %q", s.Speaker, UnknownSpeaker)))
			s.Speaker = UnknownSpeaker
		}
		if position != item.origin {
			warnings = append(warnings, newWarning(CodeOutOfOrder, s.ID, fmt.Sprintf(
				"segment arrived at position %d and belongs at position %d by start time", item.origin, position)))
		}
		if s.Confidence < opts.LowConfidenceThreshold {
			warnings = append(warnings, newWarning(CodeLowConfidence, s.ID, fmt.Sprintf(
				"confidence %.2f is below the %.2f threshold", s.Confidence, opts.LowConfidenceThreshold)))
		}
		if strings.TrimSpace(s.Text) == "" {
			warnings = append(warnings, newWarning(CodeEmptyText, s.ID, "the segment carries no text"))
		}

		segments = append(segments, s)
	}

	warnings = append(warnings, timelineWarnings(segments, opts)...)

	if len(segments) == 0 {
		warnings = append(warnings, newWarning(CodeEmptyTranscript, "",
			"the call has no segments, so there is nothing to clean and a human must confirm the call really was silent"))
	}

	SortWarnings(warnings)

	// The contract says findings are an array: a clean call serializes [] and
	// never null, so a consumer never has to distinguish "no findings" from
	// "the field is absent".
	if warnings == nil {
		warnings = []Warning{}
	}

	return Transcript{
		CallID:         in.CallID,
		AudioRef:       in.AudioRef,
		Segments:       segments,
		Warnings:       warnings,
		ReviewRequired: ReviewRequired(warnings),
	}, nil
}

// validateSegments applies the rules that make a transcript impossible to
// produce: a missing or duplicated identity, an unusable range and a
// confidence outside the unit interval.
func validateSegments(segments []Segment) error {
	seen := make(map[string]struct{}, len(segments))
	for i, s := range segments {
		if strings.TrimSpace(s.ID) == "" {
			return fmt.Errorf("%w: segment at position %d", ErrBlankSegmentID, i)
		}
		if _, dup := seen[s.ID]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateID, s.ID)
		}
		seen[s.ID] = struct{}{}

		if math.IsNaN(s.Start) || math.IsNaN(s.End) || math.IsInf(s.Start, 0) || math.IsInf(s.End, 0) {
			return fmt.Errorf("%w: segment %q is not finite", ErrInvalidTimeRange, s.ID)
		}
		if s.Start < 0 || s.End <= s.Start {
			return fmt.Errorf("%w: segment %q covers [%v,%v]", ErrInvalidTimeRange, s.ID, s.Start, s.End)
		}
		if math.IsNaN(s.Confidence) || s.Confidence < 0 || s.Confidence > 1 {
			return fmt.Errorf("%w: segment %q reports %v", ErrInvalidConfidence, s.ID, s.Confidence)
		}
	}
	return nil
}

// timelineWarnings reports overlaps and long silences between consecutive
// segments of an already ordered transcript.
func timelineWarnings(segments []Segment, opts Options) []Warning {
	var warnings []Warning
	for i := 1; i < len(segments); i++ {
		previous, current := segments[i-1], segments[i]

		if current.Start < previous.End {
			overlap := previous.End - current.Start
			warnings = append(warnings,
				newWarning(CodeOverlap, previous.ID, fmt.Sprintf("overlaps %s by %.3fs", current.ID, overlap)),
				newWarning(CodeOverlap, current.ID, fmt.Sprintf("overlaps %s by %.3fs", previous.ID, overlap)),
			)
		}

		if silence := current.Start - previous.End; silence > opts.GapSeconds {
			warnings = append(warnings, newWarning(CodeGap, current.ID, fmt.Sprintf(
				"%.3fs of silence after %s exceeds the %.1fs limit", silence, previous.ID, opts.GapSeconds)))
		}
	}
	return warnings
}

// newWarning stamps the review policy of a code onto a finding.
func newWarning(code WarningCode, segmentID, detail string) Warning {
	return NewWarning(code, segmentID, detail)
}

// IsUnknownSpeaker reports whether a speaker label carries no usable identity.
// A blank label, the placeholder written by this package and the usual
// transcription placeholders are all unknown.
func IsUnknownSpeaker(label string) bool {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "", UnknownSpeaker, "?", "-", "--", "n/a", "na", "unidentified", "unknown speaker", "speaker":
		return true
	default:
		return false
	}
}
