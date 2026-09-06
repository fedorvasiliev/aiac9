package interactive

import (
	"testing"
)

func TestAssembleRequest_TemperatureFromTemplate(t *testing.T) {
	tmpl := parsePrompt(t, "### Model\nkimi-k2.6\n\n### User Prompt\nhi\n\n### Temperature\n0.3\n")

	_, _, opts := assembleRequest(tmpl, defaultModel, "")
	if opts.Temperature == nil || *opts.Temperature != 0.3 {
		t.Fatalf("Temperature = %v, want 0.3", opts.Temperature)
	}
}

func TestAssembleRequest_InvalidTemperatureIsIgnored(t *testing.T) {
	tmpl := parsePrompt(t, "### Model\nkimi-k2.6\n\n### User Prompt\nhi\n\n### Temperature\nnot-a-number\n")

	_, _, opts := assembleRequest(tmpl, defaultModel, "")
	if opts.Temperature != nil {
		t.Fatalf("Temperature = %v, want nil for an unparsable value", *opts.Temperature)
	}
}

func TestAssembleRequest_NoTemplateHasNoTemperature(t *testing.T) {
	_, _, opts := assembleRequest(nil, defaultModel, "hello")
	if opts.Temperature != nil {
		t.Fatalf("Temperature = %v, want nil without a template", *opts.Temperature)
	}
}
