package definitions

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEdgeFailedYAMLRoundTrip_FalseSurvives(t *testing.T) {
	f := false
	e := Edge{From: "a", To: "b", When: "_loop_exhausted", Failed: &f}
	out, err := yaml.Marshal(e)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var back Edge
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal failed: %v\n%s", err, out)
	}
	if back.Failed == nil {
		t.Fatalf("Failed pointer is nil after round-trip; raw yaml:\n%s", out)
	}
	if *back.Failed != false {
		t.Fatalf("Failed value lost; got %v, want false; raw yaml:\n%s", *back.Failed, out)
	}
}

func TestEdgeFailedYAMLRoundTrip_TrueSurvives(t *testing.T) {
	tr := true
	e := Edge{From: "a", To: "b", When: "_loop_exhausted", Failed: &tr}
	out, _ := yaml.Marshal(e)
	var back Edge
	_ = yaml.Unmarshal(out, &back)
	if back.Failed == nil || *back.Failed != true {
		t.Fatalf("Failed=true lost across round-trip; raw yaml:\n%s", out)
	}
}

func TestEdgeFailedYAMLRoundTrip_AbsentStaysNil(t *testing.T) {
	e := Edge{From: "a", To: "b", When: "default"}
	out, _ := yaml.Marshal(e)
	var back Edge
	_ = yaml.Unmarshal(out, &back)
	if back.Failed != nil {
		t.Fatalf("absent Failed materialized as non-nil pointer; raw yaml:\n%s", out)
	}
}
