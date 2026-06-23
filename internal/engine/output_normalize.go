package engine

func normalizeOutputData(output *NodeOutput) {
	if output == nil || output.Data == nil {
		return
	}
	if _, ok := output.Data["project_spec"]; ok {
		return
	}
	teamsRaw, ok := output.Data["teams"]
	if !ok {
		return
	}
	switch teams := teamsRaw.(type) {
	case []any:
		if len(teams) == 0 {
			return
		}
		if teamMap, ok := teams[0].(map[string]any); ok {
			if spec, ok := teamMap["project_spec"]; ok {
				output.Data["project_spec"] = spec
			}
		}
	case []map[string]any:
		if len(teams) == 0 {
			return
		}
		if spec, ok := teams[0]["project_spec"]; ok {
			output.Data["project_spec"] = spec
		}
	}
}
