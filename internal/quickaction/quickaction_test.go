package quickaction

import (
	"testing"
)

func TestEngine_List_Empty(t *testing.T) {
	engine := NewEngine([]Action{}, "")
	actions := engine.List()
	if len(actions) != 0 {
		t.Errorf("expected 0 actions, got %d", len(actions))
	}
}

func TestEngine_List_Nil(t *testing.T) {
	engine := NewEngine(nil, "")
	actions := engine.List()
	if len(actions) != 0 {
		t.Errorf("expected 0 actions for nil slice, got %d", len(actions))
	}
}

func TestEngine_List_Populated(t *testing.T) {
	engine := NewEngine([]Action{
		{Name: "deploy", Label: "Deploy to production"},
		{Name: "review", Label: "Code review"},
	}, "")
	actions := engine.List()
	if len(actions) != 2 {
		t.Errorf("expected 2 actions, got %d", len(actions))
	}
}

func TestEngine_Get_Found(t *testing.T) {
	engine := NewEngine([]Action{
		{Name: "deploy", Label: "Deploy to production"},
		{Name: "review", Label: "Code review"},
	}, "")

	action, err := engine.Get("deploy")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if action.Name != "deploy" {
		t.Errorf("expected name 'deploy', got %s", action.Name)
	}
}

func TestEngine_Get_NotFound(t *testing.T) {
	engine := NewEngine([]Action{
		{Name: "deploy", Label: "Deploy to production"},
	}, "")

	_, err := engine.Get("unknown")
	if err == nil {
		t.Error("expected error for missing action, got nil")
	}
}

func TestEngine_BuildPrompt_Static(t *testing.T) {
	engine := NewEngine([]Action{
		{Name: "hello", Prompt: "Say hello", Agent: "琉璃"},
	}, "琉璃")

	prompt, role, err := engine.BuildPrompt("hello", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prompt != "Say hello" {
		t.Errorf("expected prompt 'Say hello', got %s", prompt)
	}
	if role != "琉璃" {
		t.Errorf("expected role '琉璃', got %s", role)
	}
}

func TestEngine_BuildPrompt_Template(t *testing.T) {
	engine := NewEngine([]Action{
		{
			Name:           "greet",
			PromptTemplate: "Hello {{.name}}!",
			Agent:          "琉璃",
		},
	}, "琉璃")

	params := map[string]any{"name": "Alice"}
	prompt, role, err := engine.BuildPrompt("greet", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prompt != "Hello Alice!" {
		t.Errorf("expected prompt 'Hello Alice!', got %s", prompt)
	}
	if role != "琉璃" {
		t.Errorf("expected role '琉璃', got %s", role)
	}
}

func TestEngine_BuildPrompt_DefaultAgent(t *testing.T) {
	engine := NewEngine([]Action{
		{Name: "hello", Prompt: "Say hello"},
	}, "defaultAgent")

	_, role, err := engine.BuildPrompt("hello", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if role != "defaultAgent" {
		t.Errorf("expected role 'defaultAgent', got %s", role)
	}
}

func TestEngine_BuildPrompt_Defaults(t *testing.T) {
	engine := NewEngine([]Action{
		{
			Name:           "greet",
			PromptTemplate: "Hello {{.name}}, you are {{.age}} years old!",
			Params: map[string]Param{
				"name": {Type: "string", Default: "Guest"},
				"age":  {Type: "number", Default: 18},
			},
			Agent: "琉璃",
		},
	}, "琉璃")

	// Only override name, age should use default.
	params := map[string]any{"name": "Bob"}
	prompt, _, err := engine.BuildPrompt("greet", params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prompt != "Hello Bob, you are 18 years old!" {
		t.Errorf("expected 'Hello Bob, you are 18 years old!', got %s", prompt)
	}

	// No params, should use all defaults.
	prompt2, _, err := engine.BuildPrompt("greet", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if prompt2 != "Hello Guest, you are 18 years old!" {
		t.Errorf("expected 'Hello Guest, you are 18 years old!', got %s", prompt2)
	}
}

func TestEngine_BuildPrompt_NoPrompt(t *testing.T) {
	engine := NewEngine([]Action{
		{Name: "empty", Label: "No prompt defined"},
	}, "")

	_, _, err := engine.BuildPrompt("empty", nil)
	if err == nil {
		t.Error("expected error for action with no prompt or template, got nil")
	}
}

func TestEngine_Search(t *testing.T) {
	engine := NewEngine([]Action{
		{Name: "deploy", Label: "Deploy to production", Shortcut: "d"},
		{Name: "review", Label: "Code review", Shortcut: "r"},
		{Name: "test", Label: "Run tests", Shortcut: "t"},
	}, "")

	// Search by name.
	results := engine.Search("deploy")
	if len(results) != 1 || results[0].Name != "deploy" {
		t.Errorf("expected 1 result 'deploy', got %d results", len(results))
	}

	// Search by label substring.
	results = engine.Search("code")
	if len(results) != 1 || results[0].Name != "review" {
		t.Errorf("expected 1 result 'review', got %d results", len(results))
	}

	// Search by label substring (case insensitive).
	results = engine.Search("PRODUCTION")
	if len(results) != 1 || results[0].Name != "deploy" {
		t.Errorf("expected 1 result 'deploy', got %d results", len(results))
	}
}

func TestEngine_Search_EmptyQuery(t *testing.T) {
	engine := NewEngine([]Action{
		{Name: "deploy", Label: "Deploy to production"},
		{Name: "review", Label: "Code review"},
	}, "")

	results := engine.Search("")
	if len(results) != 2 {
		t.Errorf("expected all 2 results for empty query, got %d", len(results))
	}
}

func TestEngine_Search_NoMatch(t *testing.T) {
	engine := NewEngine([]Action{
		{Name: "deploy", Label: "Deploy to production"},
	}, "")

	results := engine.Search("unknown")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestEngine_Search_Shortcut(t *testing.T) {
	engine := NewEngine([]Action{
		{Name: "build", Label: "Build project", Shortcut: "b"},
		{Name: "test", Label: "Run tests", Shortcut: "t"},
	}, "")

	results := engine.Search("b")
	if len(results) != 1 || results[0].Name != "build" {
		t.Errorf("expected 1 result 'build', got %d results", len(results))
	}
}
