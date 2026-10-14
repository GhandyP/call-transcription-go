package transcript

import (
	"fmt"
	"strings"
)

// ValidateOutput checks an output transcript against the input segments it was
// derived from. It is the guard that lets a model rewrite segment text without
// being able to change anything else.
//
// in must be the transcript that was handed out (the normalized one). The
// output may differ from it in three ways only:
//
//   - segment text may be rewritten (cleaned, redacted, corrected);
//   - a segment that arrived with an unidentified speaker may carry
//     UnknownSpeaker;
//   - warnings and ReviewRequired may be recomputed.
//
// Everything else is drift: a dropped, invented or reordered segment, a changed
// speaker, a moved time range, a changed confidence, a changed call id, or a
// segment whose text was blanked. Drift is reported as an error so the caller
// rejects the answer and never repairs it silently.
func ValidateOutput(in CallInput, out Transcript) error {
	if out.CallID != in.CallID {
		return fmt.Errorf("%w: call id %q does not match %q", ErrOutputDrift, out.CallID, in.CallID)
	}
	if len(out.Segments) != len(in.Segments) {
		return fmt.Errorf("%w: %d segments returned for %d source segments", ErrOutputDrift, len(out.Segments), len(in.Segments))
	}

	for i, source := range in.Segments {
		got := out.Segments[i]

		if got.ID != source.ID {
			return fmt.Errorf("%w: position %d holds segment %q, want %q", ErrOutputDrift, i, got.ID, source.ID)
		}
		if !speakerMatches(source.Speaker, got.Speaker) {
			return fmt.Errorf("%w: segment %q speaker %q does not match %q", ErrOutputDrift, source.ID, got.Speaker, source.Speaker)
		}
		if got.Start != source.Start || got.End != source.End {
			return fmt.Errorf("%w: segment %q covers [%v,%v], want [%v,%v]",
				ErrOutputDrift, source.ID, got.Start, got.End, source.Start, source.End)
		}
		if got.Confidence != source.Confidence {
			return fmt.Errorf("%w: segment %q reports confidence %v, want %v",
				ErrOutputDrift, source.ID, got.Confidence, source.Confidence)
		}
		if strings.TrimSpace(source.Text) != "" && strings.TrimSpace(got.Text) == "" {
			return fmt.Errorf("%w: segment %q had text and came back blank", ErrBlankedText, source.ID)
		}
	}
	return nil
}

// speakerMatches accepts an output speaker when it is the source label or when
// the source label carried no identity and the output names it UnknownSpeaker.
func speakerMatches(source, got string) bool {
	if source == got {
		return true
	}
	return IsUnknownSpeaker(source) && got == UnknownSpeaker
}
