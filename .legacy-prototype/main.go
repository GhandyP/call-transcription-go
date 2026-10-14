package main

import (
	"context"
	"log"
	"os"

	"github.com/GhandyP/adk-go-enterprise-cookbook/internal/adkhelper"
)

func main() {
	if err := adkhelper.Run(
		context.Background(),
		os.Args[1:],
		"case_34_call_transcription",
		"A text-only starting point for call transcription.",
		"Clean and organize the transcript text supplied by the user. Preserve speaker labels and timestamps when present, and do not claim to transcribe audio.",
	); err != nil {
		log.Fatal(err)
	}
}
