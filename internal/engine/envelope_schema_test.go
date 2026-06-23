package engine

import (
	"encoding/json"
	"reflect"
	"testing"

	"primeradiant.com/toil/internal/definitions"
)

func TestBuildEnvelopeSchema_NilDataSchema(t *testing.T) {
	schema := BuildEnvelopeSchema([]string{"done"}, nil)

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map, got %T", schema["properties"])
	}
	data, ok := props["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data to be a map, got %T", props["data"])
	}
	if data["type"] != "object" {
		t.Fatalf("expected data.type=object, got %v", data)
	}
	if _, hasProps := data["properties"]; hasProps {
		t.Fatalf("expected open data (no properties), got %v", data)
	}
}

func TestBuildEnvelopeSchema_WithDataSchema(t *testing.T) {
	dataSchema := map[string]any{
		"type":     "object",
		"required": []string{"plan"},
		"properties": map[string]any{
			"plan": map[string]any{"type": "string"},
		},
	}
	schema := BuildEnvelopeSchema([]string{"done"}, dataSchema)

	props, _ := schema["properties"].(map[string]any)
	got, _ := props["data"].(map[string]any)
	if !reflect.DeepEqual(got, dataSchema) {
		t.Fatalf("expected data schema nested verbatim, got %v", got)
	}
}

func TestBuildEnvelopeSchema_DecisionEnum(t *testing.T) {
	schema := BuildEnvelopeSchema([]string{"approved", "rejected"}, nil)

	props, _ := schema["properties"].(map[string]any)
	decision, _ := props["decision"].(map[string]any)
	enum, _ := decision["enum"].([]string)
	want := []string{"approved", "rejected"}
	if !reflect.DeepEqual(enum, want) {
		t.Fatalf("decision.enum=%v, want %v", enum, want)
	}
}

func TestBuildEnvelopeSchema_EmptyDecisions(t *testing.T) {
	schema := BuildEnvelopeSchema(nil, nil)

	props, _ := schema["properties"].(map[string]any)
	decision, _ := props["decision"].(map[string]any)
	if _, hasEnum := decision["enum"]; hasEnum {
		t.Fatalf("expected decision to have no enum when no decisions declared, got %v", decision)
	}
	if decision["type"] != "string" {
		t.Fatalf("decision.type=%v, want string", decision["type"])
	}
}

func TestBuildEnvelopeSchema_EnvelopeShape(t *testing.T) {
	schema := BuildEnvelopeSchema([]string{"done"}, nil)

	if schema["type"] != "object" {
		t.Errorf("expected top-level type=object, got %v", schema["type"])
	}
	if schema["additionalProperties"] != false {
		t.Errorf("expected additionalProperties=false, got %v", schema["additionalProperties"])
	}
	required, _ := schema["required"].([]string)
	want := []string{"decision", "message", "data", "artifacts"}
	if !reflect.DeepEqual(required, want) {
		t.Errorf("required=%v, want %v", required, want)
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range want {
		if _, ok := props[key]; !ok {
			t.Errorf("properties missing key %q", key)
		}
	}
	message, _ := props["message"].(map[string]any)
	if message["minLength"] != 1 {
		t.Errorf("message.minLength=%v, want 1", message["minLength"])
	}
}

func TestBuildRequestSchemaJSON_NilWhenNothingDeclared(t *testing.T) {
	node := &definitions.Node{}
	raw, err := BuildRequestSchemaJSON(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw != nil {
		t.Fatalf("expected nil JSON for node with no decisions and no schema, got %s", raw)
	}
}

func TestBuildRequestSchemaJSON_NilForNilNode(t *testing.T) {
	raw, err := BuildRequestSchemaJSON(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw != nil {
		t.Fatalf("expected nil JSON for nil node, got %s", raw)
	}
}

func TestBuildRequestSchemaJSON_WithDecisionsOnly(t *testing.T) {
	node := &definitions.Node{
		Decisions: definitions.StringDecisions("yes", "no"),
	}
	raw, err := BuildRequestSchemaJSON(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == nil {
		t.Fatal("expected non-nil schema JSON")
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("marshaled JSON is not parseable: %v", err)
	}
	props, _ := decoded["properties"].(map[string]any)
	decision, _ := props["decision"].(map[string]any)
	enum, _ := decision["enum"].([]any)
	if len(enum) != 2 || enum[0] != "yes" || enum[1] != "no" {
		t.Fatalf("enum=%v, want [yes no]", enum)
	}
	// Data should be open when no schema.
	data, _ := props["data"].(map[string]any)
	if data["type"] != "object" {
		t.Fatalf("data.type=%v, want object", data["type"])
	}
}

func TestBuildRequestSchemaJSON_WithFullSchema(t *testing.T) {
	node := &definitions.Node{
		Decisions: definitions.StringDecisions("done"),
		OutputsSchema: map[string]any{
			"type":     "object",
			"required": []any{"plan"},
			"properties": map[string]any{
				"plan": map[string]any{"type": "string"},
			},
		},
	}
	raw, err := BuildRequestSchemaJSON(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Round-trip: what we built should decode into something valid.
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("schema JSON not parseable: %v", err)
	}
	props, _ := decoded["properties"].(map[string]any)
	data, _ := props["data"].(map[string]any)
	if data["type"] != "object" {
		t.Fatalf("data.type=%v, want object", data["type"])
	}
	dataProps, _ := data["properties"].(map[string]any)
	if _, ok := dataProps["plan"]; !ok {
		t.Fatalf("expected data.properties.plan to be preserved")
	}
}

func TestBuildRequestSchemaJSON_OutputsSchemaOnly(t *testing.T) {
	node := &definitions.Node{
		OutputsSchema: map[string]any{"type": "object"},
	}
	raw, err := BuildRequestSchemaJSON(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if raw == nil {
		t.Fatal("expected non-nil JSON when outputs_schema is set")
	}
}
