# Legacy prototype (case 34)

Source: case `34-call-transcription` of the author's private ADK cookbook
(`github.com/GhandyP/adk-go-enterprise-cookbook`). Both files are kept verbatim
as provenance for this repository:

- `README.legacy.md` — the case source plan. Every requirement quoted by the ODD
  feature document (`odd/tasks/call-transcription-go.md`) comes from it.
- `main.go` — the original prototype entry point. It imports the cookbook's
  private `internal/adkhelper` package, so it does not build outside the
  cookbook and is not part of this module.

This directory is hidden on purpose: the Go tool ignores directories whose name
starts with `.`, so `go build ./...` never tries to compile it.
