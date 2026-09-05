package interactive

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/fedorvasiliev/aiac9/internal/config"
)

func TestRun_ExitsCleanlyOnImmediateEOF(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	pw.Close() // nothing written: the read end sees EOF right away
	defer pr.Close()

	var out bytes.Buffer
	if err := Run(context.Background(), &config.Config{}, pr, &out); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Model") {
		t.Fatalf("expected the model select prompt to have been printed, got:\n%s", out.String())
	}
}

func TestRun_ReportsMissingAPIKeyAndLoopsBackToSelect(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pr.Close()

	go func() {
		defer pw.Close()
		// Step 1: pick option 4 (kimi-k2.6) by number; Step T: a prompt.
		// No MOONSHOT_API_KEY is configured, so Complete fails locally
		// without any network call, Run reports it, and loops back to
		// Step 1 — where this pipe's EOF then ends the wizard.
		pw.Write([]byte("4\nkakoy segodnya den?\n"))
	}()

	var out bytes.Buffer
	if err := Run(context.Background(), &config.Config{}, pr, &out); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "MOONSHOT_API_KEY is not set") {
		t.Fatalf("expected the missing-API-key error to be printed, got:\n%s", out.String())
	}
}
