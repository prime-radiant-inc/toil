package api

import (
	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/document"
)

// docLoader is a thin alias for document.RunStoreLoader kept so that the
// api package's internal call sites need no change.
type docLoader = document.RunStoreLoader

func newDocLoader(runsDir string) *docLoader {
	return document.NewRunStoreLoader(runsDir)
}

// docRegistry is a thin alias for document.WorkflowRegistry.
type docRegistry = document.WorkflowRegistry

func newDocRegistry(bundle *definitions.Bundle, loader *docLoader) *docRegistry {
	return document.NewWorkflowRegistry(bundle, loader)
}
