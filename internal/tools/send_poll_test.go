package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSendPollToolExecuteSendsPoll(t *testing.T) {
	var sentQuestion string
	var sentOptions []string
	tool := &SendPollTool{
		chatID: 123,
		send: func(chatID int64, question string, options []string, isAnonymous bool, allowsMultipleAnswers bool) error {
			if chatID != 123 {
				t.Fatalf("expected chat id 123, got %d", chatID)
			}
			sentQuestion = question
			sentOptions = append([]string(nil), options...)
			if !isAnonymous {
				t.Fatalf("expected anonymous poll by default")
			}
			if allowsMultipleAnswers {
				t.Fatalf("did not expect multiple answers by default")
			}
			return nil
		},
	}

	result, err := tool.Execute(context.Background(), `{"question":"Лучшая видеоигра?","options":["Скайрим","Гта","Лучше в стену посмотреть"]}`)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	if sentQuestion != "Лучшая видеоигра?" {
		t.Fatalf("unexpected question: %q", sentQuestion)
	}
	if len(sentOptions) != 3 || sentOptions[2] != "Лучше в стену посмотреть" {
		t.Fatalf("unexpected options: %#v", sentOptions)
	}
	if !tool.WasSent() {
		t.Fatal("expected tool to record successful send")
	}

	var payload sendPollResult
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if !payload.Sent {
		t.Fatal("expected sent=true in result")
	}
}

func TestSendPollToolExecuteRejectsTooFewOptions(t *testing.T) {
	tool := &SendPollTool{chatID: 123, send: func(int64, string, []string, bool, bool) error {
		t.Fatal("send should not be called")
		return nil
	}}

	_, err := tool.Execute(context.Background(), `{"question":"choose","options":["only one"]}`)
	if err == nil {
		t.Fatal("expected error for too few options")
	}
}
