package definitions

func FindNode(workflow *Workflow, nodeID string) *Node {
	if workflow == nil {
		return nil
	}
	for i := range workflow.Nodes {
		if workflow.Nodes[i].ID == nodeID {
			return &workflow.Nodes[i]
		}
	}
	return nil
}
