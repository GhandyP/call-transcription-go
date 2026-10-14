# call-transcription-go

Clean and organize supplied call transcript segments with **Google ADK Go (v2)**,
preserving segment identity, speaker labels and timestamps — and without ever
claiming that audio was transcribed.

The model is the last step, never the whole pipeline. Everything that can be
decided without a model is plain Go with tests: the segment contract, timestamp
normalization, overlap and gap detection, confidence handling, unknown-speaker
handling, redaction and output validation. One ADK agent then rewrites the text
of each segment under a JSON schema that only exposes `id` and `text`, and its
answer is **rejected instead of repaired** when it drops, invents, repeats or
blanks a segment.

```text
CallInput JSON ──▶ transcript.Normalize ──▶ cleanup.Clean ──▶ ADK agent ──▶ validated Transcript
                   (contract rules)        (text + redaction)  (one turn,     (ValidateOutput)
                                                                schema-bound)
```

## What this repository does not do

- It does not ingest audio. This delivery is text-only: the STT adapter boundary
  ships with an **offline** implementation, not a vendor integration, and no
  adapter in the repository ever opens a recording.
- It does not store transcripts and never writes to disk.
- It never claims to have transcribed audio, and a segment that carries no text
  says so instead of being invented.
- It does not decide anything by itself: a transcript that needs a human is
  marked `reviewRequired`, and the command still exits `0`.

## Requirements

- Go 1.26.6 or newer (`GOTOOLCHAIN=auto` resolves the toolchain)
- `GOOGLE_API_KEY` for the model path only. `-baseline` runs without
  credentials, without network access and without a model.

## Quick start

```bash
go build ./...

# deterministic path: no model, no credentials, no network
./call-transcription-go -baseline examples/call.json

# model path: one ADK agent turn over the cleaned text
export GOOGLE_API_KEY="your-key"
./call-transcription-go examples/call.json

# an input that only references a recording needs the offline transcriber
printf '%s' '{"callID":"call-4419","audioRef":"s3://recordings/call-4419.wav","segments":[]}' \
  | ./call-transcription-go -baseline -stt-fixture examples/stt_fixture.json -
```

### Flags

| Flag | Meaning |
| --- | --- |
| `-baseline` | Run the deterministic path only and never call a model. |
| `-model string` | Gemini model for the cleanup agent (default `gemini-flash-latest`, also settable through `ADK_MODEL`). |
| `-stt-fixture string` | Offline STT fixture used when the input carries an `audioRef` and no segments. |

`-baseline` combined with an explicit `-model` is a **usage error** rather than a
silently ignored flag: the caller has to know which path ran. Use `-` as the
input path to read the document from standard input.

### Exit codes

| Code | Meaning |
| --- | --- |
| `0` | A transcript was produced. `reviewRequired` tells you whether a human must look at it. |
| `1` | The input or the run failed: unusable document, missing credential, rejected model answer. |
| `2` | Usage error: unknown flag, wrong number of arguments, contradictory flags. |

A transcript that needs review is **data, not a failure**, which is why the
review gate never changes the exit code.

## The contract

Input — one call, an optional recording *reference* and the segments an upstream
STT step produced:

```json
{
  "callID": "call-4417",
  "audioRef": "s3://recordings/2026/call-4417.wav",
  "segments": [
    { "id": "s1", "speaker": "Customer", "start": 0, "end": 6,
      "text": "I need to change my plan before the next bill.", "confidence": 0.94 }
  ]
}
```

Output — the same segments, with the findings the rules produced. This is real
output, produced by `./call-transcription-go -baseline examples/sensitive_call.json`:

```json
{
  "callID": "call-4418",
  "audioRef": "s3://recordings/2026/call-4418.wav",
  "segments": [
    { "id": "s1", "speaker": "Customer", "start": 0, "end": 6,
      "text": "My email is [redacted:email] and my card is [redacted:card].", "confidence": 0.92 },
    { "id": "s2", "speaker": "Agent", "start": 6, "end": 10,
      "text": "I have noted the reference you gave me.", "confidence": 0.9 },
    { "id": "s3", "speaker": "Customer", "start": 45, "end": 49,
      "text": "This whole process is shit, honestly.", "confidence": 0.4 }
  ],
  "warnings": [
    { "code": "redacted", "segmentID": "s1",
      "detail": "replaced 2 sensitive value(s) matched by the email, card rule(s)", "requiresReview": true },
    { "code": "gap", "segmentID": "s3",
      "detail": "35.000s of silence after s2 exceeds the 30.0s limit", "requiresReview": false },
    { "code": "low_confidence", "segmentID": "s3",
      "detail": "confidence 0.40 is below the 0.70 threshold", "requiresReview": true },
    { "code": "profanity", "segmentID": "s3",
      "detail": "the profanity policy matched 1 term(s); the words are kept", "requiresReview": false }
  ],
  "reviewRequired": true
}
```

Identity is a source fact: `id`, `speaker`, `start`, `end` and `confidence` come
out exactly as they went in, and a model cannot change them because
`ValidateOutput` rejects any output that moves them.

## Findings

| Code | Raised for | Gates review |
| --- | --- | --- |
| `out_of_order_timestamp` | A segment whose position did not match its start time. The output is sorted by start time. | no |
| `overlap` | Two segments whose ranges intersect. Raised on both. | no |
| `gap` | More than 30 s of silence between consecutive segments. | no |
| `unknown_speaker` | A label that carries no identity (blank, `?`, `unknown`, …). The label becomes `"unknown"`. | no |
| `empty_text` | A segment with no text. | no |
| `profanity` | The configured policy matched a whole word, inflections included (`shit` matches `shitting`, not `shitake`). The words are kept unless the policy asks for redaction. | no |
| `redacted` | A sensitive value was replaced. The finding names the rule, never the value. | **yes** |
| `low_confidence` | Below 0.70 by default. | **yes** |
| `empty_transcript` | The call has no segments at all. | **yes** |

Errors are reserved for input that cannot produce a transcript at all: a missing
`callID`, a blank or duplicated segment id, a negative or inverted range, a
confidence outside `[0,1]`, a present but blank `audioRef`.

## Recording, retention and the human gate

- A recording is only ever **referenced**. `stt.Retention` is part of the adapter
  interface, the only supported policy is keeping nothing, and the command
  refuses a transcriber that claims otherwise. There is no code path in this
  repository that opens, downloads, copies or stores audio.
- The model sees the least it can work with: the prompt carries `callID`, segment
  ids and cleaned text. Speaker labels, timestamps, confidences, findings and the
  audio reference never leave the process, and the output schema does not offer
  the model a way to return them.
- Redaction is defence in depth, **not a seal**: a rule-based filter cannot
  recognise every possible secret, so sensitive material should not be pasted
  into a transcript in the first place. Verify a document before you share it.
- A transcript with `reviewRequired: true` (low confidence, redacted content, or
  a call with no segments) must be reviewed by a human before it is used,
  published or stored. This release ships no store and no `save_transcript`
  tool: that decision needs retention and access rules first, and it stays
  approval-gated.

## Verification

The suite runs entirely offline — an injected `model.LLM` replaces Gemini, so
the whole model layer is exercised without credentials and without network:

```bash
gofmt -l .          # empty
go vet ./...        # clean
go build ./...      # clean
go test ./... -count=1
```

Observed at the commit that introduced the examples: **94 passing assertions, 0
failures** across the five packages. The tests cover the contract rules and their
fixtures (overlaps, gaps, unknown speakers, low confidence, out-of-order
timestamps), the nine-row model-drift table, the redaction leak check, the
profanity word-boundary rule, the STT boundary and its offline transcriber, the
agent prompt and schema reduction, and the command's exit codes.

Honest scope: **the live Gemini call was not executed in this environment**, because
no API key was available. What is verified is the whole model path around it —
instruction, output schema, prompt reduction, response parsing, rejection of a
bad answer, session-per-call wiring and error propagation. Run the model path
with your own `GOOGLE_API_KEY` to exercise the network hop.

## Repository layout

```text
main.go                        the command: flags, exit codes, pipeline wiring
internal/transcript/           the contract: types, rules, ValidateOutput guard
internal/cleanup/              deterministic text cleanup, redaction, profanity policy
internal/agent/                the ADK Go (v2) layer: instruction, schema, parser, agent
adapters/stt/                  the recording-to-text boundary and its offline transcriber
examples/                      input documents and an STT fixture used by this README
odd/tasks/                     the ODD feature document this delivery was built from
.legacy-prototype/             the original case files, kept as provenance
```

## Provenance

This repository is a from-scratch production of case 34 of the author's ADK
cookbook. The original case files are preserved under
[`.legacy-prototype/`](.legacy-prototype/README.md): its README is the source
plan, and its `main.go` referenced the cookbook's private helper package, which
is why the prototype never built outside the cookbook.

## License

MIT — see [LICENSE](LICENSE).
