package interactive

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_AnswersEachQuestion(t *testing.T) {
	in := strings.NewReader("вопрос 1\nвопрос 2\nвопрос 3\n")
	var out bytes.Buffer

	if err := Run(in, &out); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	answers := 0
	for _, line := range strings.Split(out.String(), "\n") {
		if line == "> да" || line == "> нет" {
			answers++
		}
	}
	if answers != 3 {
		t.Fatalf("expected 3 answers, got %d in output:\n%s", answers, out.String())
	}
}

func TestRun_ExitStopsLoop(t *testing.T) {
	in := strings.NewReader("вопрос\nexit\nвопрос после выхода\n")
	var out bytes.Buffer

	if err := Run(in, &out); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if strings.Contains(out.String(), "после выхода") {
		t.Fatal("Run should stop reading after exit")
	}
}

func TestAnswer_Distribution(t *testing.T) {
	const n = 100000
	yes := 0
	for i := 0; i < n; i++ {
		if answer() == "да" {
			yes++
		}
	}

	ratio := float64(yes) / float64(n)
	if ratio < 0.75 || ratio > 0.79 {
		t.Fatalf("expected ~0.77 'да' ratio, got %f", ratio)
	}
}
