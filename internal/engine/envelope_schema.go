package engine

import (
	"encoding/json"

	"primeradiant.com/toil/internal/definitions"
)

// BuildEnvelopeSchema produces the full runner-visible JSON Schema by wrapping
// the node's outputs_schema (which describes only `data`) in the envelope
// {decision, message, data, artifacts}.
//
// If dataSchema is nil, data is described as an open object (type: object).
// Returns a map suitable for json.Marshal.
func BuildEnvelopeSchema(decisions []string, dataSchema map[string]any) map[string]any {
	decisionProp := map[string]any{keyType: jsonTypeString}
	if len(decisions) > 0 {
		decisionProp["enum"] = append([]string{}, decisions...)
	}

	var data any
	if dataSchema != nil {
		data = dataSchema
	} else {
		data = map[string]any{keyType: jsonTypeObject}
	}

	return map[string]any{
		keyType: jsonTypeObject,
		keyProperties: map[string]any{
			fieldDecision:  decisionProp,
			fieldMessage:   map[string]any{keyType: jsonTypeString, "minLength": 1},
			fieldData:      data,
			fieldArtifacts: map[string]any{keyType: "array", keyItems: map[string]any{keyType: jsonTypeString}},
		},
		keyRequired:            []string{fieldDecision, fieldMessage, fieldData, fieldArtifacts},
		"additionalProperties": false,
	}
}

// BuildRequestSchemaJSON returns marshaled envelope JSON for a node, or nil
// when there's nothing to describe (no decisions and no outputs_schema).
func BuildRequestSchemaJSON(node *definitions.Node) ([]byte, error) {
	if node == nil || (len(node.Decisions) == 0 && node.OutputsSchema == nil) {
		return nil, nil
	}
	envelope := BuildEnvelopeSchema(node.Decisions.IDs(), node.OutputsSchema)
	return json.Marshal(envelope)
}
