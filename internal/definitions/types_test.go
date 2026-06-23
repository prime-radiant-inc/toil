package definitions

import "testing"

func TestForEachBodyField(t *testing.T) {
	fe := &ForEach{
		List: "input.items",
		Item: "item",
		Body: "template_node",
	}
	if fe.Body != "template_node" {
		t.Fatalf("expected Body = %q, got %q", "template_node", fe.Body)
	}
}
