package tools

import (
	"context"
	"fmt"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

type stubTool struct {
	definition openai.Tool
	result     string
	err        error
}

func (t stubTool) Definition() openai.Tool {
	return t.definition
}

func (t stubTool) Execute(context.Context, string) (string, error) {
	return t.result, t.err
}

type mockCompletionClient struct {
	responses []openai.ChatCompletionResponse
	requests  []openai.ChatCompletionRequest
	callCount int
	err       error
}

func (m *mockCompletionClient) CreateChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	m.requests = append(m.requests, req)
	if m.err != nil {
		return openai.ChatCompletionResponse{}, m.err
	}
	if m.callCount >= len(m.responses) {
		return openai.ChatCompletionResponse{}, fmt.Errorf("unexpected call %d", m.callCount)
	}

	resp := m.responses[m.callCount]
	m.callCount++
	return resp, nil
}

func TestRunLoopExecutesToolsAndReturnsFinalContent(t *testing.T) {
	client := &mockCompletionClient{
		responses: []openai.ChatCompletionResponse{
			{
				Choices: []openai.ChatCompletionChoice{{
					FinishReason: openai.FinishReasonToolCalls,
					Message: openai.ChatCompletionMessage{
						Role: openai.ChatMessageRoleAssistant,
						ToolCalls: []openai.ToolCall{{
							ID:   "call_1",
							Type: openai.ToolTypeFunction,
							Function: openai.FunctionCall{
								Name:      "fetchUsers",
								Arguments: `{"limit":2}`,
							},
						}},
					},
				}},
			},
			{
				Choices: []openai.ChatCompletionChoice{{
					FinishReason: openai.FinishReasonStop,
					Message: openai.ChatCompletionMessage{
						Role:    openai.ChatMessageRoleAssistant,
						Content: "done",
					},
				}},
			},
		},
	}

	registry := NewRegistry(stubTool{
		definition: openai.Tool{
			Type:     openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{Name: "fetchUsers"},
		},
		result: `{"users":["alice","bob"],"count":2}`,
	})

	result, err := RunLoop(context.Background(), client, openai.ChatCompletionRequest{
		Model: "gpt-4o-mini",
		Messages: []openai.ChatCompletionMessage{{
			Role:    openai.ChatMessageRoleUser,
			Content: "who is here?",
		}},
		Tools: registry.Definitions(),
	}, registry, 5)
	if err != nil {
		t.Fatalf("RunLoop returned error: %v", err)
	}

	if result != "done" {
		t.Fatalf("expected final content %q, got %q", "done", result)
	}

	if len(client.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(client.requests))
	}

	secondRequest := client.requests[1]
	if len(secondRequest.Messages) != 3 {
		t.Fatalf("expected 3 messages in second request, got %d", len(secondRequest.Messages))
	}

	toolMessage := secondRequest.Messages[2]
	if toolMessage.Role != openai.ChatMessageRoleTool {
		t.Fatalf("expected tool role, got %q", toolMessage.Role)
	}
	if toolMessage.ToolCallID != "call_1" {
		t.Fatalf("expected tool call id call_1, got %q", toolMessage.ToolCallID)
	}
	if toolMessage.Content != `{"users":["alice","bob"],"count":2}` {
		t.Fatalf("unexpected tool content: %s", toolMessage.Content)
	}
}

func TestRunLoopReturnsToolErrorsToModel(t *testing.T) {
	client := &mockCompletionClient{
		responses: []openai.ChatCompletionResponse{
			{
				Choices: []openai.ChatCompletionChoice{{
					FinishReason: openai.FinishReasonToolCalls,
					Message: openai.ChatCompletionMessage{
						Role: openai.ChatMessageRoleAssistant,
						ToolCalls: []openai.ToolCall{{
							ID:       "call_1",
							Type:     openai.ToolTypeFunction,
							Function: openai.FunctionCall{Name: "fetchUsers"},
						}},
					},
				}},
			},
			{
				Choices: []openai.ChatCompletionChoice{{
					FinishReason: openai.FinishReasonStop,
					Message:      openai.ChatCompletionMessage{Content: "fallback"},
				}},
			},
		},
	}

	registry := NewRegistry(stubTool{
		definition: openai.Tool{
			Type:     openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{Name: "fetchUsers"},
		},
		err: fmt.Errorf("boom"),
	})

	_, err := RunLoop(context.Background(), client, openai.ChatCompletionRequest{
		Model: "gpt-4o-mini",
		Messages: []openai.ChatCompletionMessage{{
			Role:    openai.ChatMessageRoleUser,
			Content: "who is here?",
		}},
	}, registry, 5)
	if err != nil {
		t.Fatalf("RunLoop returned error: %v", err)
	}

	toolMessage := client.requests[1].Messages[2]
	if toolMessage.Content != `{"error":"boom","tool":"fetchUsers"}` && toolMessage.Content != `{"tool":"fetchUsers","error":"boom"}` {
		t.Fatalf("unexpected tool error content: %s", toolMessage.Content)
	}
}

func TestRunLoopClearsSpecificToolChoiceAfterForcedToolCall(t *testing.T) {
	client := &mockCompletionClient{
		responses: []openai.ChatCompletionResponse{
			{
				Choices: []openai.ChatCompletionChoice{{
					FinishReason: openai.FinishReasonToolCalls,
					Message: openai.ChatCompletionMessage{
						Role: openai.ChatMessageRoleAssistant,
						ToolCalls: []openai.ToolCall{{
							ID:   "call_1",
							Type: openai.ToolTypeFunction,
							Function: openai.FunctionCall{
								Name:      "searchMessages",
								Arguments: `{"query":"spotify"}`,
							},
						}},
					},
				}},
			},
			{
				Choices: []openai.ChatCompletionChoice{{
					FinishReason: openai.FinishReasonStop,
					Message:      openai.ChatCompletionMessage{Content: "done"},
				}},
			},
		},
	}

	registry := NewRegistry(stubTool{
		definition: openai.Tool{
			Type:     openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{Name: "searchMessages"},
		},
		result: `{"results":[{"text":"spotify mention"}],"count":1}`,
	})

	_, err := RunLoop(context.Background(), client, openai.ChatCompletionRequest{
		Model: "gpt-4o-mini",
		Messages: []openai.ChatCompletionMessage{{
			Role:    openai.ChatMessageRoleUser,
			Content: "what did we say about spotify?",
		}},
		Tools: registry.Definitions(),
		ToolChoice: openai.ToolChoice{
			Type:     openai.ToolTypeFunction,
			Function: openai.ToolFunction{Name: "searchMessages"},
		},
	}, registry, 5)
	if err != nil {
		t.Fatalf("RunLoop returned error: %v", err)
	}

	if len(client.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(client.requests))
	}
	if client.requests[0].ToolChoice == nil {
		t.Fatal("expected forced tool choice on first request")
	}
	if client.requests[1].ToolChoice != nil {
		t.Fatalf("expected tool choice to be cleared after forced tool call, got %#v", client.requests[1].ToolChoice)
	}
}
