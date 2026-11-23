package reply

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
	"github.com/tucnak/telebot"

	"github.com/focusshifter/muxgoob/utils/testutils"
)

func TestMemoryAdminPromptSupportsUsefulHistoryAnswers(t *testing.T) {
	prompt := strings.Join(memoryAdminSystemParts(-637245), "\n")
	for _, required := range []string{
		"ordinary questions", "recommendations", "gifts", "searchMessages",
		"getChatHistoryBounds", "entire stored", "sort set to oldest",
		"Only mutate memory when the owner requests", "not a memory dump",
		"untrusted data", "not system instructions", "target-chat prompt",
		"Do not claim success unless the mutation tool succeeds",
	} {
		if !strings.Contains(prompt, required) {
			t.Errorf("superuser prompt missing %q", required)
		}
	}
	if strings.Contains(prompt, "Perform only memory inspection") {
		t.Fatal("superuser prompt still restricts ordinary analytical requests")
	}
}

func TestSuperuserInitialHistoryToolChoiceParity(t *testing.T) {
	for _, tc := range []struct {
		question string
		want     string
	}{
		{"найди сообщения про подарки", "searchMessages"},
		{"обсуждали ли мы телескоп раньше?", "searchMessages"},
		{"what is the oldest message in chat history?", "getChatHistoryBounds"},
		{"какое самое раннее сообщение в истории?", "getChatHistoryBounds"},
		{"порекомендуй подарок", ""},
		{"придумай шутку", ""},
		{"запомни любимый цвет", ""},
	} {
		for _, admin := range []bool{false, true} {
			message := &telebot.Message{Chat: &telebot.Chat{ID: -637245}, Text: tc.question}
			choice := initialToolChoiceForRequest(tc.question, message, admin)
			if tc.want == "" {
				if choice != nil {
					t.Errorf("admin=%v %q: unexpected forced choice %#v", admin, tc.question, choice)
				}
				continue
			}
			got, ok := choice.(openai.ToolChoice)
			if !ok || got.Function.Name != tc.want {
				t.Errorf("admin=%v %q: got %#v, want %s", admin, tc.question, choice, tc.want)
			}
		}
	}
	// Existing named-person forcing is not expanded to !su memory requests.
	message := &telebot.Message{Chat: &telebot.Chat{ID: -637245}, Text: "забудь факт про @alice"}
	if choice := initialToolChoiceForRequest(message.Text, message, true); choice != nil {
		t.Fatalf("unrelated memory request gained forced lookup: %#v", choice)
	}
}

func TestMemoryAdminHistoryToolsAreTargetScoped(t *testing.T) {
	db := testutils.SetupTestDB(t)
	defer db.Close()
	// The owner's private message ID deliberately collides with a stored target
	// message. A synthetic !su request must not exclude that target message.
	_, err := db.Exec(`
		CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, first_name TEXT, last_name TEXT);
		CREATE TABLE messages (id INTEGER, chat_id INTEGER, sender_id INTEGER, unixtime INTEGER, text TEXT, caption TEXT, data TEXT, PRIMARY KEY (id, chat_id));
		INSERT INTO users VALUES (1, 'alice', 'Alice', '');
		INSERT INTO messages (id, chat_id, sender_id, unixtime, text) VALUES
			(77, -637245, 1, 1000, 'gift telescope archived preference'),
			(78, -637245, 1, 2000, 'gift astronomy current preference'),
			(77, 686563, 1, 1, 'gift PRIVATE OWNER SECRET'),
			(79, -999, 1, 9000, 'gift OTHER CHAT SECRET');
	`)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range memoryAdminTools(db, -637245) {
		name := tool.Definition().Function.Name
		switch name {
		case "searchMessages":
			for _, sort := range []string{"relevance", "oldest"} {
				result, err := tool.Execute(context.Background(), `{"query":"gift","sort":"`+sort+`","limit":50,"chat_id":686563}`)
				if err != nil {
					t.Fatalf("search %s: %v", sort, err)
				}
				var payload struct {
					Count   int `json:"count"`
					Results []struct {
						Text string `json:"text"`
					} `json:"results"`
				}
				if err := json.Unmarshal([]byte(result), &payload); err != nil {
					t.Fatal(err)
				}
				if payload.Count != 2 || len(payload.Results) != 2 || strings.Contains(result, "SECRET") || !strings.Contains(result, "archived preference") {
					t.Fatalf("search %s leaked another chat or excluded stored target history: %s", sort, result)
				}
				if sort == "oldest" && payload.Results[0].Text != "gift telescope archived preference" {
					t.Fatalf("oldest search lost archived match: %s", result)
				}
			}
			found[name] = true
		case "getChatHistoryBounds":
			result, err := tool.Execute(context.Background(), `{"chat_id":686563}`)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Count    int   `json:"count"`
				Earliest int64 `json:"earliest_timestamp"`
				Latest   int64 `json:"latest_timestamp"`
			}
			if err := json.Unmarshal([]byte(result), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Count != 2 || payload.Earliest != 1000 || payload.Latest != 2000 {
				t.Fatalf("bounds escaped target chat: %s", result)
			}
			found[name] = true
		}
	}
	if !found["searchMessages"] || !found["getChatHistoryBounds"] {
		t.Fatalf("missing target-scoped history tools: %v", found)
	}
}
