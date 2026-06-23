package document

import (
	"primeradiant.com/toil/internal/state"
)

// fakeLoader returns canned RunStates for tests. Shared by tree_test.go and
// any other test in this package that needs an in-memory Loader.
type fakeLoader struct {
	runs   map[string]*state.RunState
	events map[string][]state.Event // optional; keyed by runID
}

func (f *fakeLoader) LoadRun(id string) (*state.RunState, error) {
	if rs, ok := f.runs[id]; ok {
		return rs, nil
	}
	return nil, ErrRunNotFound
}

func (f *fakeLoader) ChildRuns(parentID string) []string {
	var out []string
	for id, rs := range f.runs {
		if rs.ParentRun == parentID {
			out = append(out, id)
		}
	}
	return out
}

// LoadEvents satisfies the EventLoader interface. Returns the pre-loaded events
// for the run, or nil if none are registered.
func (f *fakeLoader) LoadEvents(runID string) []state.Event {
	if f.events == nil {
		return nil
	}
	return f.events[runID]
}
