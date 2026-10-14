# call-transcription-go

Clean and organize supplied call transcript segments with Google ADK Go (v2),
preserving speaker labels, segment identity and timestamps — and without ever
claiming that audio was transcribed.

The model is the last step, never the whole pipeline. Everything that can be
checked deterministically — the segment contract, timestamp normalization,
overlap and gap detection, confidence handling, redaction and output validation
— is plain Go with tests. One ADK agent rewrites the text of each segment under
a JSON schema, and its answer is rejected instead of repaired when it drops,
invents or reorders a segment.

Status: under construction. The work is tracked in
[`odd/tasks/call-transcription-go.md`](odd/tasks/call-transcription-go.md).

## What this repository is not

- It does not ingest audio: this delivery is text-only, and the STT adapter
  boundary ships with an offline fake implementation, not a vendor integration.
- It does not store transcripts and does not persist audio. Retention and
  access rules must be decided before any store is added.
- It never claims to have transcribed audio. If a segment carries no text, the
  output says so instead of inventing one.

## Requirements

- Go 1.26.6 or newer (`GOTOOLCHAIN=auto` picks the cached toolchain)
- `GOOGLE_API_KEY` for the model path only; the deterministic path runs offline

## Development

```bash
gofmt -l .
go vet ./...
go build ./...
go test ./... -count=1
```

## Provenance

This repository is a from-scratch production of case 34 of the author's ADK
cookbook. The original case files are preserved under
[`.legacy-prototype/`](.legacy-prototype/README.md) — the legacy README is the
source plan, and its legacy `main.go` referenced the cookbook's private helper
package, which is why the prototype never built outside the cookbook.

## License

MIT — see [LICENSE](LICENSE).
