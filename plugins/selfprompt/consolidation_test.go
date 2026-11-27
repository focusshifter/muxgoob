package selfprompt

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/focusshifter/muxgoob/utils/facts"
	openai "github.com/sashabaranov/go-openai"
)

// All requests are intercepted; an unexpected extra completion fails the test.
func consolidationReplies(t *testing.T, replies ...string) (int64, *int) {
	t.Helper()
	id := setupAllModels(t)
	if err := SetModel(id, "fixture/model"); err != nil {
		t.Fatal(err)
	}
	calls := new(int)
	http.DefaultTransport = allmodelsTransport(func(r *http.Request) (*http.Response, error) {
		index := *calls
		*calls++
		var req openai.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "fixture/model" {
			t.Fatalf("model=%q", req.Model)
		}
		if index >= len(replies) {
			t.Errorf("unexpected consolidation call %d", *calls)
			return nil, fmt.Errorf("unexpected completion")
		}
		if replies[index] == "HTTP_ERROR" {
			return nil, fmt.Errorf("fixture failure")
		}
		data, _ := json.Marshal(map[string]interface{}{"choices": []interface{}{map[string]interface{}{"message": map[string]string{"role": "assistant", "content": replies[index]}}}})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	return *id, calls
}

func profileFixture(count, width int) string {
	var b strings.Builder
	b.WriteString("Identity:\n- Lives in Helsinki and works as an engineer\n\nAppearance:\n- Wears rectangular glasses\n\nInterests:\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&b, "- Collects artifact%03d with catalog entry %s\n", i, strings.Repeat(fmt.Sprintf("%03d", i), width))
	}
	return strings.TrimSpace(b.String())
}
func chatFixture(count, width int) string {
	var b strings.Builder
	b.WriteString("Reply style:\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&b, "- For ritual%03d use exact response %s\n", i, strings.Repeat(fmt.Sprintf("%03d", i), width))
	}
	b.WriteString("\nStable context:\n- Weekly puzzle tournament on Fridays\n\nAvoid:\n- Do not reveal puzzle answers without permission")
	return b.String()
}

func TestConsolidationSafetyThreshold(t *testing.T) {
	for _, n := range []int{2200, 3000, 16*1024 - 1, 16 * 1024, 16*1024 + 1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			if got, want := shouldConsolidateFacts(strings.Repeat("x", n)), n >= 16*1024; got != want {
				t.Fatalf("bytes=%d consolidate=%v want=%v", n, got, want)
			}
		})
	}
	if shouldConsolidateFacts(strings.Repeat("- distinct bullet\n", 100)) {
		t.Fatal("bullet count is not a consolidation trigger")
	}
	if DefaultCompactChars() != 16*1024 || DefaultCompactBullets() != int(^uint(0)>>1) {
		t.Fatal("compatibility accessors must expose byte safeguard and disabled bullet trigger")
	}
}

func TestOrdinaryProfileUpdatePreservesManyBulletsWithoutConsolidation(t *testing.T) {
	id, calls := consolidationReplies(t, "Interests:\n+ Enjoys underwater photography near coral reefs")
	current := profileFixture(25, 30)
	got := (&SelfPromptPlugin{}).generateUserFacts(id, activeChatUser{ID: 17, Name: "alice"}, "alice: I enjoy underwater photography", current)
	if !strings.Contains(got, "underwater photography") {
		t.Fatalf("new fact lost: %s", got)
	}
	before, after := facts.ParseDossier(current), facts.ParseDossier(got)
	if len(after.Interests) != len(before.Interests)+1 || len(after.Appearance) != len(before.Appearance) {
		t.Fatalf("facts clipped: interests %d -> %d", len(before.Interests), len(after.Interests))
	}
	for _, bullet := range before.Interests {
		if !strings.Contains(got, bullet) {
			t.Fatalf("lost existing fact %q", bullet)
		}
	}
	if *calls != 1 {
		t.Fatalf("calls=%d want=1", *calls)
	}
}

func TestOrdinaryChatUpdatePreservesManyBulletsWithoutConsolidation(t *testing.T) {
	id, calls := consolidationReplies(t, "Avoid:\n+ Never disclose addresses from private messages")
	current := chatFixture(25, 30)
	got := (&SelfPromptPlugin{}).generateNewPrompt(id, "chat history", current)
	if !strings.Contains(got, "Never disclose addresses") {
		t.Fatalf("new guidance lost: %s", got)
	}
	if len(facts.ParseChatPrompt(got).ReplyStyle) != 25 {
		t.Fatalf("existing guidance clipped: %s", got)
	}
	if *calls != 1 {
		t.Fatalf("calls=%d want=1", *calls)
	}
}

func TestNoOpChatDeltaDoesNotConsolidateEvenAboveThreshold(t *testing.T) {
	for _, delta := range []string{"Reply style:\n+ The chat discusses topics", "Stable context:\n+ Weekly puzzle tournament on Fridays"} {
		t.Run(delta, func(t *testing.T) {
			id, calls := consolidationReplies(t, delta)
			current := chatFixture(80, 70)
			got := (&SelfPromptPlugin{}).generateNewPrompt(id, "chat history", current)
			if got != current {
				t.Fatal("no-op delta must return original unchanged")
			}
			if *calls != 1 {
				t.Fatalf("calls=%d want=1", *calls)
			}
		})
	}
}

func TestOversizedConsolidationFailurePreservesMergedCandidate(t *testing.T) {
	t.Run("person", func(t *testing.T) {
		id, calls := consolidationReplies(t, "Interests:\n+ Enjoys underwater photography near coral reefs", "HTTP_ERROR")
		current := profileFixture(80, 70)
		got := (&SelfPromptPlugin{}).generateUserFacts(id, activeChatUser{ID: 17, Name: "alice"}, "history", current)
		if !strings.Contains(got, "underwater photography") || len(facts.ParseDossier(got).Interests) != 81 {
			t.Fatal("failed shortener lost merged facts")
		}
		if *calls != 2 {
			t.Fatalf("calls=%d want=2", *calls)
		}
	})
	t.Run("chat", func(t *testing.T) {
		id, calls := consolidationReplies(t, "Avoid:\n+ Never disclose addresses from private messages", "HTTP_ERROR")
		current := chatFixture(80, 70)
		got := (&SelfPromptPlugin{}).generateNewPrompt(id, "history", current)
		if !strings.Contains(got, "Never disclose addresses") || len(facts.ParseChatPrompt(got).ReplyStyle) != 80 {
			t.Fatal("failed shortener lost merged guidance")
		}
		if *calls != 2 {
			t.Fatalf("calls=%d want=2", *calls)
		}
	})
}

func TestChatShortenerRejectsInvalidEmptyAndNonShorterResults(t *testing.T) {
	current := chatFixture(80, 70)
	for _, candidate := range []string{"", "commentary only", "Reply style:\n- terse", current + "\n- Additional guidance", "Reply style:\n\nStable context:\n\nAvoid:", "Reply style:\n- terse\n\nStable context:\n- one detail\n\nAvoid:\n- one mistake"} {
		t.Run(fmt.Sprintf("candidate-%d", len(candidate)), func(t *testing.T) {
			id, _ := consolidationReplies(t, candidate, candidate)
			if got := consolidateChatPrompt(id, current); got != current {
				t.Fatal("invalid/empty/non-shorter result replaced guidance")
			}
		})
	}
}

func TestLargeProfileConsolidatesAndPreservesPinnedAppearance(t *testing.T) {
	current := profileFixture(80, 70)
	candidate := facts.ParseDossier(profileFixture(80, 20))
	candidate.Appearance = []string{"Invented replacement appearance"}
	candidate.Interests = append(candidate.Interests, "Enjoys underwater photography near coral reefs")
	id, calls := consolidationReplies(t, "Interests:\n+ Enjoys underwater photography near coral reefs", facts.RenderDossier(candidate))
	got := (&SelfPromptPlugin{}).generateUserFacts(id, activeChatUser{ID: 17, Name: "alice"}, "history", current)
	if !strings.Contains(got, "underwater photography") || strings.Contains(got, "Invented replacement") || !strings.Contains(got, "Wears rectangular glasses") {
		t.Fatal("large-profile shortener lost new facts or changed pinned appearance")
	}
	if len(got) >= len(current) || len(facts.ParseDossier(got).Interests) != 81 {
		t.Fatal("valid shorter profile was not retained without clipping")
	}
	if *calls != 2 {
		t.Fatalf("calls=%d want=2", *calls)
	}
}

func TestPersonShortenerRejectsInvalidEmptyAndNonShorterResults(t *testing.T) {
	current := profileFixture(80, 70)
	for _, candidate := range []string{"", "commentary only", "Identity:\n- minimal\n\nInterests:\n- one fact", current + "\n- Additional fact"} {
		t.Run(fmt.Sprintf("candidate-%d", len(candidate)), func(t *testing.T) {
			id, _ := consolidationReplies(t, candidate, candidate)
			if got := consolidatePersonFacts(id, "alice", current); got != current {
				t.Fatal("unsafe shortener replaced profile")
			}
		})
	}
}

func TestProfileUpdatePreservesUncappedAppearance(t *testing.T) {
	current := facts.ParseDossier(profileFixture(20, 20))
	for i := 0; i < 20; i++ {
		current.Appearance = append(current.Appearance, fmt.Sprintf("Pinned visual trait %02d", i))
	}
	id, calls := consolidationReplies(t, "Interests:\n+ Enjoys underwater photography near coral reefs")
	got := (&SelfPromptPlugin{}).generateUserFacts(id, activeChatUser{ID: 17, Name: "alice"}, "history", facts.RenderDossier(current))
	if len(facts.ParseDossier(got).Appearance) != len(current.Appearance) {
		t.Fatal("appearance was clipped")
	}
	for _, appearance := range current.Appearance {
		if !strings.Contains(got, appearance) {
			t.Fatalf("lost pinned appearance %q", appearance)
		}
	}
	if *calls != 1 {
		t.Fatalf("calls=%d want=1", *calls)
	}
}

func TestLargeChatGuidanceConsolidatesWithValidShorterResult(t *testing.T) {
	shortPrompt := facts.ParseChatPrompt(chatFixture(80, 20))
	shortPrompt.Avoid = append(shortPrompt.Avoid, "Never disclose addresses from private messages")
	short := facts.RenderChatPrompt(shortPrompt)
	id, calls := consolidationReplies(t, "Avoid:\n+ Never disclose addresses from private messages", short)
	got := (&SelfPromptPlugin{}).generateNewPrompt(id, "history", chatFixture(80, 70))
	if got != short {
		t.Fatalf("valid shorter result not accepted: %s", got)
	}
	if *calls != 2 {
		t.Fatalf("calls=%d want=2", *calls)
	}
}
