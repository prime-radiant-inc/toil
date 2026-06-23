package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"primeradiant.com/toil/internal/definitions"
	"primeradiant.com/toil/internal/state"
)

func TestGetTemplatesDevModeReloadsFresh(t *testing.T) {
	t.Setenv("TOIL_DEV", "1")
	t.Chdir("../..") // repo root

	s := &Server{
		templates: nil, // no cached templates
		devMode:   true,
	}

	// First call should parse from disk
	tmpl1 := s.getTemplates()
	if tmpl1 == nil {
		t.Fatal("getTemplates (dev) returned nil on first call")
	}
	if tmpl1.Lookup("overview.html") == nil {
		t.Error("getTemplates (dev) missing overview.html")
	}

	// Second call should also parse from disk (fresh instance)
	tmpl2 := s.getTemplates()
	if tmpl2 == nil {
		t.Fatal("getTemplates (dev) returned nil on second call")
	}
	// In dev mode, each call returns a new template instance
	if tmpl1 == tmpl2 {
		t.Error("getTemplates (dev) returned same pointer — should be fresh parse each call")
	}
}

func TestGetTemplatesProdModeReturnsCached(t *testing.T) {
	t.Setenv("TOIL_DEV", "")

	cached, _ := LoadTemplates()
	s := &Server{
		templates: cached,
		devMode:   false,
	}

	tmpl1 := s.getTemplates()
	tmpl2 := s.getTemplates()
	if tmpl1 != tmpl2 {
		t.Error("getTemplates (prod) returned different pointers — should be cached")
	}
}

func TestHumanizeStatus(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"running", "Running"},
		{statusCompleted, "Completed"},
		{"failed", "Failed"},
		{"paused", "Paused"},
		{"awaiting_approval", "Awaiting Approval"},
		{"skipped", "Skipped"},
		{statusPending, "Pending"},
		{"", ""},
	}
	for _, tc := range tests {
		got := humanizeStatus(tc.input)
		if got != tc.want {
			t.Errorf("humanizeStatus(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStoryStatusClass(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"draft", "bg-gray-400/30 text-gray-600"},
		{"refined", "bg-amber-400 text-white"},
		{"ready", "bg-accent text-white"},
		{"in-progress", "bg-blue-500 text-white"},
		{"done", "bg-green-600 text-white"},
		{"unknown", "bg-gray-400/30 text-gray-600"},
		{"", "bg-gray-400/30 text-gray-600"},
		{"READY", "bg-accent text-white"},
	}
	for _, tc := range tests {
		got := storyStatusClass(tc.input)
		if got != tc.want {
			t.Errorf("storyStatusClass(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseStoryCards(t *testing.T) {
	stories := []any{
		map[string]any{
			"id":          "story-1",
			"title":       "User Login",
			"description": "Users can log in with email and password",
			"acceptance_criteria": []any{
				"Login form validates email format",
				"Incorrect password shows error",
			},
		},
		map[string]any{
			"id":      "story-2",
			"title":   "Dashboard",
			"status":  "ready",
			"content": "---\ntitle: Dashboard\n---\nDashboard body content here.",
		},
	}

	cards := parseStoryCards(stories)
	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(cards))
	}

	if cards[0].ID != "story-1" {
		t.Errorf("card 0 ID = %q, want story-1", cards[0].ID)
	}
	if cards[0].Title != "User Login" {
		t.Errorf("card 0 Title = %q, want User Login", cards[0].Title)
	}
	if cards[0].Body == "" {
		t.Error("card 0 Body should not be empty")
	}
	if cards[0].BodyHTML == "" {
		t.Error("card 0 BodyHTML should not be empty")
	}

	// Card 2 should have frontmatter stripped
	if cards[1].Body != "Dashboard body content here." {
		t.Errorf("card 1 Body = %q, want stripped content", cards[1].Body)
	}
	if cards[1].Status != "ready" {
		t.Errorf("card 1 Status = %q, want ready", cards[1].Status)
	}
}

func TestParseStoryCardsNilInput(t *testing.T) {
	cards := parseStoryCards(nil)
	if cards != nil {
		t.Errorf("expected nil, got %v", cards)
	}
}

func TestStripYAMLFrontmatter(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"---\ntitle: foo\n---\nBody here", "Body here"},
		{"No frontmatter", "No frontmatter"},
		{"---\nonly start", "---\nonly start"},
		{"---\n---\n", ""},
	}
	for _, tc := range tests {
		got := stripYAMLFrontmatter(tc.input)
		if got != tc.want {
			t.Errorf("stripYAMLFrontmatter(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestBuildNodeSummaries(t *testing.T) {
	wf := &definitions.Workflow{
		Nodes: []definitions.Node{
			{ID: "plan", Role: "planner"},
			{ID: "build"},
		},
	}
	rs := state.NewRunState("run-1", "test-wf", nil)
	rs.WithNode("plan", func(n *state.NodeState) {
		n.Status = statusCompleted
	})

	nodes := buildNodeSummaries(wf, rs)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].ID != "plan" {
		t.Errorf("node 0 ID = %q, want plan", nodes[0].ID)
	}
	if nodes[0].Label != "plan (planner)" {
		t.Errorf("node 0 Label = %q, want 'plan (planner)'", nodes[0].Label)
	}
	if nodes[0].Status != statusCompleted {
		t.Errorf("node 0 Status = %q, want completed", nodes[0].Status)
	}
	if nodes[1].Status != statusPending {
		t.Errorf("node 1 Status = %q, want pending", nodes[1].Status)
	}
}

func TestBuildNodeSummariesNilWorkflow(t *testing.T) {
	nodes := buildNodeSummaries(nil, nil)
	if nodes != nil {
		t.Errorf("expected nil, got %v", nodes)
	}
}

func TestTimeAgo(t *testing.T) {
	now := time.Now()
	tests := []struct {
		input time.Time
		want  string
	}{
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-90 * time.Second), "1m ago"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-90 * time.Minute), "1h ago"},
		{now.Add(-3 * time.Hour), "3h ago"},
		{now.Add(-36 * time.Hour), "1d ago"},
		{now.Add(-72 * time.Hour), "3d ago"},
		{time.Time{}, ""},
	}
	for _, tc := range tests {
		got := timeAgo(tc.input)
		if got != tc.want {
			t.Errorf("timeAgo(%v) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestDevModeReturnsTrue(t *testing.T) {
	t.Setenv("TOIL_DEV", "1")
	if !devMode() {
		t.Error("devMode() = false, want true when TOIL_DEV=1")
	}
}

func TestDevModeReturnsFalse(t *testing.T) {
	t.Setenv("TOIL_DEV", "")
	if devMode() {
		t.Error("devMode() = true, want false when TOIL_DEV is empty")
	}
}

func TestLoadTemplatesDevMode(t *testing.T) {
	t.Setenv("TOIL_DEV", "1")
	t.Chdir("../..") // repo root — matches Docker working_dir: /app

	tmpl, err := LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates (dev) error: %v", err)
	}
	if tmpl == nil {
		t.Fatal("LoadTemplates (dev) returned nil")
	}
	if tmpl.Lookup("overview.html") == nil {
		t.Error("dev-mode templates missing overview.html")
	}
}

func TestStaticFileHandlerDevMode(t *testing.T) {
	t.Setenv("TOIL_DEV", "1")
	t.Chdir("../..") // repo root

	handler := StaticFileHandler()
	if handler == nil {
		t.Fatal("StaticFileHandler (dev) returned nil")
	}

	req := httptest.NewRequest("GET", "/static/js/toil.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("StaticFileHandler (dev) status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("StaticFileHandler (dev) returned empty body")
	}
}

func TestStaticFileHandlerProdMode(t *testing.T) {
	t.Setenv("TOIL_DEV", "")

	handler := StaticFileHandler()
	req := httptest.NewRequest("GET", "/static/js/toil.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("StaticFileHandler (prod) status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("StaticFileHandler (prod) returned empty body")
	}
}
