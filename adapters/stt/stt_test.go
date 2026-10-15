package stt_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GhandyP/call-transcription-go/adapters/stt"
	"github.com/GhandyP/call-transcription-go/internal/transcript"
)

func loadFake(t *testing.T) *stt.Fake {
	t.Helper()
	fake, err := stt.NewFakeFromFile(filepath.Join("testdata", "fake_segments.json"))
	if err != nil {
		t.Fatalf("NewFakeFromFile() error = %v, want nil", err)
	}
	return fake
}

func TestFakeImplementsTranscriber(t *testing.T) {
	var _ stt.Transcriber = stt.NewFake(nil)
}

func TestFakeStampsStableIDsAndChangesNothingElse(t *testing.T) {
	fake := loadFake(t)
	ctx := context.Background()
	req := stt.Request{CallID: "call-1", AudioRef: "s3://recordings/call-1.wav", Language: "es-AR"}

	first, err := fake.Transcribe(ctx, req)
	if err != nil {
		t.Fatalf("Transcribe() error = %v, want nil", err)
	}
	second, err := fake.Transcribe(ctx, req)
	if err != nil {
		t.Fatalf("Transcribe() second error = %v, want nil", err)
	}

	// The fixture keeps the order it arrived in: an out-of-order fixture must
	// stay out of order, because ordering is the contract's job.
	wantIDs := []string{"s1", "s7", "s3"}
	if len(first.Segments) != len(wantIDs) {
		t.Fatalf("len(Segments) = %d, want %d", len(first.Segments), len(wantIDs))
	}
	for i, wantID := range wantIDs {
		if first.Segments[i].ID != wantID {
			t.Errorf("segment %d ID = %q, want %q", i, first.Segments[i].ID, wantID)
		}
		if first.Segments[i] != second.Segments[i] {
			t.Errorf("segment %d differs between runs: %+v then %+v", i, first.Segments[i], second.Segments[i])
		}
	}

	if got, want := first.Segments[1].Speaker, "?"; got != want {
		t.Errorf("Speaker = %q, want the fixture label %q untouched", got, want)
	}
	if got, want := first.Segments[0].Text, "Thanks for calling support."; got != want {
		t.Errorf("Text = %q, want %q", got, want)
	}
	if got, want := first.Provider, stt.FakeProvider; got != want {
		t.Errorf("Provider = %q, want %q", got, want)
	}
}

func TestFakeHonorsContextCancellation(t *testing.T) {
	want := transcript.Segment{ID: "s1", Speaker: "Agent", Start: 0, End: 1, Text: "Hi.", Confidence: 0.9}
	for _, tt := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		err  error
	}{
		{"cancelled", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}, context.Canceled},
		{"expired deadline", func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Unix(0, 0))
		}, context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := []transcript.Segment{want}
			fake := stt.NewFake(source)
			req := stt.Request{CallID: "call-1", AudioRef: "fixture://audio"}
			originalReq := req
			ctx, cancel := tt.ctx()
			defer cancel()
			got, err := fake.Transcribe(ctx, req)
			if !errors.Is(err, tt.err) {
				t.Errorf("Transcribe() error = %v, want %v", err, tt.err)
			}
			if got.Provider != "" || len(got.Segments) != 0 {
				t.Errorf("Transcribe() returned partial data on cancellation: %+v", got)
			}
			if req != originalReq || source[0] != want {
				t.Fatal("Transcribe() mutated the source request or segments")
			}
			// A rejected call must not consume or change the offline fixture.
			next, err := fake.Transcribe(context.Background(), req)
			if err != nil || next.Provider != stt.FakeProvider || len(next.Segments) != 1 || next.Segments[0] != want {
				t.Fatalf("next Transcribe() = (%+v, %v), want the untouched fixture", next, err)
			}
		})
	}
}

func TestFakeNeverOpensTheRecording(t *testing.T) {
	fake := loadFake(t)

	result, err := fake.Transcribe(context.Background(), stt.Request{
		CallID:   "call-1",
		AudioRef: "/nonexistent/path/that/cannot/exist.wav",
	})
	if err != nil {
		t.Fatalf("Transcribe() error = %v, want nil: only the reference is used", err)
	}
	if len(result.Segments) == 0 {
		t.Error("Transcribe() returned no segments")
	}
	if got, want := fake.Retention(), stt.RetentionNone; got != want {
		t.Errorf("Retention() = %v, want %v", got, want)
	}
}

func TestTranscribeRejectsARequestWithoutIdentity(t *testing.T) {
	fake := loadFake(t)
	ctx := context.Background()

	tests := []struct {
		name string
		req  stt.Request
		want error
	}{
		{
			name: "missing call id",
			req:  stt.Request{AudioRef: "s3://recordings/call-1.wav"},
			want: stt.ErrMissingCallID,
		},
		{
			name: "missing audio reference",
			req:  stt.Request{CallID: "call-1"},
			want: stt.ErrMissingAudioRef,
		},
		{
			name: "blank audio reference",
			req:  stt.Request{CallID: "call-1", AudioRef: "   "},
			want: stt.ErrMissingAudioRef,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fake.Transcribe(ctx, tt.req)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Transcribe() error = %v, want %v", err, tt.want)
			}
			if len(result.Segments) != 0 {
				t.Errorf("Transcribe() returned %d segments with an error, want none", len(result.Segments))
			}
		})
	}
}

func TestFakeHandsEveryDefectToTheNormalizer(t *testing.T) {
	fake := loadFake(t)

	result, err := fake.Transcribe(context.Background(), stt.Request{
		CallID:   "call-1",
		AudioRef: "s3://recordings/call-1.wav",
	})
	if err != nil {
		t.Fatalf("Transcribe() error = %v, want nil", err)
	}

	out, err := transcript.Normalize(transcript.CallInput{
		CallID:   "call-1",
		Segments: result.Segments,
	}, transcript.DefaultOptions())
	if err != nil {
		t.Fatalf("Normalize() error = %v, want nil", err)
	}

	if got, want := []string{out.Segments[0].ID, out.Segments[1].ID, out.Segments[2].ID}, []string{"s1", "s3", "s7"}; !equalStrings(got, want) {
		t.Errorf("normalized order = %v, want %v", got, want)
	}

	codes := map[transcript.WarningCode]bool{}
	for _, w := range out.Warnings {
		codes[w.Code] = true
	}
	for _, want := range []transcript.WarningCode{transcript.CodeOutOfOrder, transcript.CodeUnknownSpeaker, transcript.CodeLowConfidence} {
		if !codes[want] {
			t.Errorf("warnings %v do not include %s", out.Warnings, want)
		}
	}
	if got, want := out.Segments[2].Speaker, transcript.UnknownSpeaker; got != want {
		t.Errorf("Speaker = %q, want %q", got, want)
	}
}

func TestNewFakeFromJSONRejectsAMalformedFixture(t *testing.T) {
	if _, err := stt.NewFakeFromJSON([]byte("{not json")); err == nil {
		t.Fatal("NewFakeFromJSON() error = nil, want a decode error")
	}
	if _, err := stt.NewFakeFromFile(filepath.Join("testdata", "does-not-exist.json")); err == nil {
		t.Fatal("NewFakeFromFile() error = nil, want a read error")
	}
	if _, err := os.ReadFile(filepath.Join("testdata", "fake_segments.json")); err != nil {
		t.Fatalf("the fixture used by these tests is missing: %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
