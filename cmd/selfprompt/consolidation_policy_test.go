package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/utils/facts"
)

func mockMemoryDeltaCLI(t *testing.T, output string) (int64, *int) {
	t.Helper()
	id := setupAllModels(t)
	oldModel := modelOverride
	t.Cleanup(func() { modelOverride = oldModel })
	modelOverride = "allmodels:vendor/test-memory"
	calls := new(int)
	http.DefaultTransport = allmodelsTransport(func(r *http.Request) (*http.Response, error) {
		*calls++
		body, _ := json.Marshal(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"finish_reason": "stop", "message": map[string]interface{}{"role": "assistant", "content": output}}}})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})
	return *id, calls
}

func TestCLIPersonUpdateDoesNotRewriteOrClipOrdinaryProfile(t *testing.T) {
	id, calls := mockMemoryDeltaCLI(t, "Interests:\n+ Owns a vintage ThinkPad X220 laptop.")
	d := &facts.Dossier{}
	for i := 0; i < 50; i++ {
		d.Identity = append(d.Identity, fmt.Sprintf("Independent durable personal fact marker %03d from past conversations", i))
	}
	current := facts.RenderDossier(d)
	got, err := generateUserFacts(id, activeChatUser{ID: 42, Name: "Alice"}, "Alice: I own a vintage ThinkPad X220 laptop.", current)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("calls=%d, want one extraction", *calls)
	}
	parsed := facts.ParseDossier(got)
	if len(parsed.Identity) != 50 || len(parsed.Interests) != 1 {
		t.Fatalf("facts lost: identity=%d interests=%d", len(parsed.Identity), len(parsed.Interests))
	}
}

func TestCLIChatUpdateDoesNotRewriteOrClipGuidance(t *testing.T) {
	id, calls := mockMemoryDeltaCLI(t, "Reply style:\n+ Use SI units when discussing hardware measurements.")
	p := &facts.ChatPrompt{}
	for i := 0; i < 20; i++ {
		p.Avoid = append(p.Avoid, fmt.Sprintf("Preserve independent safety instruction marker %03d", i))
	}
	p.ReplyStyle = []string{"Keep technical explanations clear and grounded."}
	got := generateNewPrompt(id, "Alice: use SI units", facts.RenderChatPrompt(p))
	if *calls != 1 {
		t.Fatalf("calls=%d, want one extraction", *calls)
	}
	parsed := facts.ParseChatPrompt(got)
	if len(parsed.Avoid) != 20 || len(parsed.ReplyStyle) != 2 {
		t.Fatalf("guidance lost: %#v", parsed)
	}
}

func TestCLINoopLargeChatDeltaSkipsConsolidation(t *testing.T) {
	const instruction = "Use SI units when discussing hardware measurements."
	id, calls := mockMemoryDeltaCLI(t, "Reply style:\n+ "+instruction)
	p := &facts.ChatPrompt{ReplyStyle: []string{instruction}, Avoid: []string{strings.Repeat("Keep durable guard. ", 1100)}}
	current := facts.RenderChatPrompt(p)
	got := generateNewPrompt(id, "nothing new", current)
	if *calls != 1 || got != current {
		t.Fatalf("no-op changed prompt or made extra request: calls=%d", *calls)
	}
}

func TestCLIInvalidRareChatConsolidationPreservesMergedGuidance(t *testing.T) {
	id, calls := mockMemoryDeltaCLI(t, "malformed shortener output")
	current := facts.RenderChatPrompt(&facts.ChatPrompt{ReplyStyle: []string{strings.Repeat("Important detailed guidance. ", 800)}, Avoid: []string{"Do not disclose secrets."}})
	got := consolidateChatPrompt(id, current)
	if got != current || *calls != 2 {
		t.Fatalf("unsafe shortener accepted or retry unbounded: calls=%d", *calls)
	}
}
