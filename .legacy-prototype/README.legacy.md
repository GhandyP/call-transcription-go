# 34 — Call transcription

## Goal

Clean and organize supplied transcript text while preserving speakers and timestamps, without claiming that audio has been transcribed.

## Run the current prototype

From this case directory:

```bash
export GOOGLE_API_KEY="your-key"
go run .
```

`internal/adkhelper.Run` uses ADK Go v2.2.0, Gemini, and `gemini-flash-latest`; set `ADK_MODEL` to override the model.

Sample prompt:

```text
Clean these call transcript segments and preserve their speaker labels.
```

## What exists now

- `main.go (exists)` sends pasted transcript segments to one focused Gemini ADK agent.
- The output is free-form text; segment IDs, speakers, timestamps, confidence, and redactions are not validated.
- This is intentionally text-only: it does not ingest audio, call Whisper or another STT service, or store a transcript.

## Missing elements

- **Contract:** Define `CallInput{callID, audioRef?, segments[]}` -> `Transcript{segments[{id,speaker,start,end,text,confidence}], warnings[]}`.
- **ADK workflow:** Add an STT adapter before a transcript-cleanup agent, with segment schema and call session; keep a future `save_transcript` tool approval-gated.
- **Domain boundary:** Add audio storage, Whisper/STT, speaker diarization, timestamp normalization, and transcript repository adapters.
- **Fixtures and validation:** Cover overlaps, unknown speakers, gaps, low confidence, profanity policy, and out-of-order timestamps.
- **Safety:** Protect recordings and transcript access, retain raw audio only by policy, and require human review for low-confidence or sensitive calls.

## Target structure

```text
34-call-transcription/
├── main.go (exists)
├── README.md (exists)
├── workflow.go (next)
├── schema.go (next)
├── adapters/stt.go (next)
├── testdata/segments.json (next)
└── main_test.go (next)
```

## Implementation order

1. Define segment, speaker, timestamp, confidence, and warning schemas.
2. Make text cleanup deterministic and preserve segment identity.
3. Add STT/diarization adapters and a call session.
4. Test overlaps, gaps, unknown speakers, and low-confidence segments.
5. Add protected persistence only after retention and review rules are approved.

## Done when

- [ ] Every output segment retains a source ID, speaker, and valid time range.
- [ ] Low-confidence, overlapping, and unknown-speaker content is flagged.
- [ ] Transcript fixtures exercise ordering and redaction policies.
- [ ] Audio access and persistence follow retention rules and human review gates.

## Next improvement

Stabilize the typed segment-cleanup path before adding Whisper or audio storage.
