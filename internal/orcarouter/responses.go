package orcarouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

func (c *Client) responses(ctx context.Context, req openai.ChatCompletionRequest) (out openai.ChatCompletionResponse, err error) {
	body, err := responsesRequest(req)
	if err != nil {
		return out, err
	}
	var raw responsesResponse
	if err = c.exchange(ctx, "POST", "/responses", body, &raw); err != nil {
		return out, err
	}
	return raw.chatResponse()
}
func responsesRequest(req openai.ChatCompletionRequest) (map[string]any, error) {
	if req.N > 1 {
		return nil, errors.New("orcarouter: Responses online supports only n=1")
	}
	if len(req.Functions) > 0 || req.FunctionCall != nil {
		return nil, errors.New("orcarouter: Responses online requires tools, not legacy functions")
	}
	var input []any
	for _, m := range req.Messages {
		if m.FunctionCall != nil || m.Role == "function" {
			return nil, errors.New("orcarouter: legacy function history unsupported; use tool calls")
		}
		if m.Content != "" && len(m.MultiContent) > 0 {
			return nil, errors.New("orcarouter: both content and multipart content set")
		}
		if m.Role == "tool" {
			if m.ToolCallID == "" {
				return nil, errors.New("orcarouter: tool result missing call ID")
			}
			if len(m.MultiContent) > 0 {
				return nil, errors.New("orcarouter: multipart tool result unsupported")
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Content})
			continue
		}
		switch m.Role {
		case "system", "developer", "user", "assistant":
		default:
			return nil, errors.New("orcarouter: unsupported message role")
		}
		var content []any
		textType := "input_text"
		if m.Role == "assistant" {
			textType = "output_text"
		}
		if m.Content != "" {
			content = append(content, map[string]any{"type": textType, "text": m.Content})
		}
		for _, p := range m.MultiContent {
			switch p.Type {
			case openai.ChatMessagePartTypeText:
				content = append(content, map[string]any{"type": textType, "text": p.Text})
			case openai.ChatMessagePartTypeImageURL:
				if p.ImageURL == nil || p.ImageURL.URL == "" {
					return nil, errors.New("orcarouter: empty image URL")
				}
				part := map[string]any{"type": "input_image", "image_url": p.ImageURL.URL}
				if p.ImageURL.Detail != "" {
					part["detail"] = p.ImageURL.Detail
				}
				content = append(content, part)
			default:
				return nil, errors.New("orcarouter: unsupported multipart content type")
			}
		}
		if m.Refusal != "" {
			if m.Role != "assistant" {
				return nil, errors.New("orcarouter: refusal requires assistant role")
			}
			content = append(content, map[string]any{"type": "refusal", "refusal": m.Refusal})
		}
		if len(content) > 0 {
			input = append(input, map[string]any{"type": "message", "role": m.Role, "content": content})
		} else if len(m.ToolCalls) == 0 {
			input = append(input, map[string]any{"type": "message", "role": m.Role, "content": []any{map[string]any{"type": textType, "text": ""}}})
		}
		for _, call := range m.ToolCalls {
			if m.Role != "assistant" || call.Type != openai.ToolTypeFunction || call.ID == "" || call.Function.Name == "" {
				return nil, errors.New("orcarouter: invalid function call history")
			}
			input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
		}
	}
	if input == nil {
		input = []any{}
	}
	tools := []any{map[string]any{"type": "web_search"}}
	for _, t := range req.Tools {
		if t.Type != openai.ToolTypeFunction || t.Function == nil {
			return nil, errors.New("orcarouter: unsupported Responses tool")
		}
		f := t.Function
		entry := map[string]any{"type": "function", "name": f.Name, "parameters": f.Parameters}
		if f.Description != "" {
			entry["description"] = f.Description
		}
		if f.Strict {
			entry["strict"] = true
		}
		tools = append(tools, entry)
	}
	body := map[string]any{"model": req.Model, "input": input, "tools": tools, "stream": false, "store": false}
	if req.ToolChoice != nil {
		choice, err := responsesToolChoice(req.ToolChoice)
		if err != nil {
			return nil, err
		}
		body["tool_choice"] = choice
	}
	tokens := req.MaxCompletionTokens
	if tokens == 0 {
		tokens = req.MaxTokens
	}
	if tokens > 0 {
		body["max_output_tokens"] = tokens
	}
	if req.Temperature != 0 {
		body["temperature"] = req.Temperature
	}
	if req.TopP != 0 {
		body["top_p"] = req.TopP
	}
	if req.ReasoningEffort != "" {
		body["reasoning"] = map[string]any{"effort": req.ReasoningEffort}
	}
	if req.ParallelToolCalls != nil {
		body["parallel_tool_calls"] = req.ParallelToolCalls
	}
	if req.Metadata != nil {
		body["metadata"] = req.Metadata
	}
	if req.User != "" {
		body["user"] = req.User
	}
	if req.ResponseFormat != nil {
		format := map[string]any{"type": req.ResponseFormat.Type}
		if s := req.ResponseFormat.JSONSchema; s != nil {
			format["name"] = s.Name
			format["schema"] = s.Schema
			format["strict"] = s.Strict
			if s.Description != "" {
				format["description"] = s.Description
			}
		}
		body["text"] = map[string]any{"format": format}
	}
	return body, nil
}
func responsesToolChoice(choice any) (any, error) {
	if s, ok := choice.(string); ok {
		switch s {
		case "auto", "none", "required":
			return s, nil
		}
		return nil, errors.New("orcarouter: unsupported Responses tool_choice string")
	}
	b, err := json.Marshal(choice)
	if err != nil {
		return nil, errors.New("orcarouter: invalid tool_choice")
	}
	var v struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(b, &v) != nil || v.Type != "function" {
		return nil, errors.New("orcarouter: unsupported Responses tool_choice")
	}
	name := v.Name
	if name == "" {
		name = v.Function.Name
	}
	if name == "" {
		return nil, errors.New("orcarouter: forced tool_choice missing function name")
	}
	return map[string]any{"type": "function", "name": name}, nil
}

type responsesResponse struct {
	ID         string `json:"id"`
	Model      string `json:"model"`
	Status     string `json:"status"`
	Created    int64  `json:"created_at"`
	Incomplete struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Status    string `json:"status"`
		Content   []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		Input         int                             `json:"input_tokens"`
		Output        int                             `json:"output_tokens"`
		Total         int                             `json:"total_tokens"`
		InputDetails  *openai.PromptTokensDetails     `json:"input_tokens_details"`
		OutputDetails *openai.CompletionTokensDetails `json:"output_tokens_details"`
	} `json:"usage"`
}

func (r responsesResponse) chatResponse() (out openai.ChatCompletionResponse, err error) {
	finish := openai.FinishReasonStop
	switch r.Status {
	case "completed":
	case "incomplete":
		switch r.Incomplete.Reason {
		case "max_output_tokens":
			finish = openai.FinishReasonLength
		case "content_filter":
			finish = openai.FinishReasonContentFilter
		default:
			return out, errors.New("orcarouter: response incomplete for unsupported reason")
		}
	default:
		return out, fmt.Errorf("orcarouter: response status %q", safeStatus(r.Status))
	}
	message := openai.ChatCompletionMessage{Role: "assistant"}
	var text strings.Builder
	for _, item := range r.Output {
		switch item.Type {
		case "message":
			for _, p := range item.Content {
				switch p.Type {
				case "output_text":
					text.WriteString(p.Text)
				case "refusal":
					message.Refusal += p.Refusal
				}
			}
		case "function_call":
			if r.Status != "completed" || (item.Status != "" && item.Status != "completed") {
				return out, errors.New("orcarouter: incomplete function call; not safe to execute")
			}
			if item.CallID == "" || item.Name == "" || !json.Valid([]byte(item.Arguments)) {
				return out, errors.New("orcarouter: invalid function call in response")
			}
			message.ToolCalls = append(message.ToolCalls, openai.ToolCall{ID: item.CallID, Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: item.Name, Arguments: item.Arguments}})
			// Built-in tool calls/reasoning are provider managed; they are not host functions.
		}
	}
	message.Content = text.String()
	if len(message.ToolCalls) > 0 {
		finish = openai.FinishReasonToolCalls
	}
	if message.Content == "" && message.Refusal == "" && len(message.ToolCalls) == 0 && r.Status == "completed" {
		return out, errors.New("orcarouter: empty completed Responses output")
	}
	out.ID = r.ID
	out.Model = r.Model
	out.Created = r.Created
	out.Object = "chat.completion"
	out.Usage = openai.Usage{PromptTokens: r.Usage.Input, CompletionTokens: r.Usage.Output, TotalTokens: r.Usage.Total, PromptTokensDetails: r.Usage.InputDetails, CompletionTokensDetails: r.Usage.OutputDetails}
	out.Choices = []openai.ChatCompletionChoice{{Index: 0, Message: message, FinishReason: finish}}
	return out, nil
}
func safeStatus(status string) string {
	switch status {
	case "failed", "in_progress", "queued", "cancelled", "incomplete", "completed", "":
		return status
	default:
		return "unknown"
	}
}
