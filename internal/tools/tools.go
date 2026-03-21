package tools

import (
	"context"
	"fmt"
	"log"

	openai "github.com/sashabaranov/go-openai"
)

const defaultMaxIterations = 5

type Tool interface {
	Definition() openai.Tool
	Execute(ctx context.Context, args string) (string, error)
}

type ChatCompletionCreator interface {
	CreateChatCompletion(ctx context.Context, request openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error)
}

type Registry struct {
	tools map[string]Tool
	defs  []openai.Tool
}

func NewRegistry(tools ...Tool) *Registry {
	registry := &Registry{
		tools: make(map[string]Tool, len(tools)),
		defs:  make([]openai.Tool, 0, len(tools)),
	}

	for _, tool := range tools {
		if tool == nil {
			continue
		}

		def := tool.Definition()
		if def.Function == nil || def.Function.Name == "" {
			continue
		}

		registry.tools[def.Function.Name] = tool
		registry.defs = append(registry.defs, def)
	}

	return registry
}

func (r *Registry) Definitions() []openai.Tool {
	if r == nil || len(r.defs) == 0 {
		return nil
	}

	defs := make([]openai.Tool, len(r.defs))
	copy(defs, r.defs)
	return defs
}

func (r *Registry) Execute(ctx context.Context, toolCall openai.ToolCall) string {
	if r == nil {
		return toolError(toolCall.Function.Name, "tool registry is nil")
	}

	tool, ok := r.tools[toolCall.Function.Name]
	if !ok {
		return toolError(toolCall.Function.Name, "unknown tool")
	}

	result, err := tool.Execute(ctx, toolCall.Function.Arguments)
	if err != nil {
		return toolError(toolCall.Function.Name, err.Error())
	}

	return result
}

func RunLoop(
	ctx context.Context,
	client ChatCompletionCreator,
	req openai.ChatCompletionRequest,
	registry *Registry,
	maxIterations int,
) (string, error) {
	if client == nil {
		return "", fmt.Errorf("chat completion client is nil")
	}

	if maxIterations <= 0 {
		maxIterations = defaultMaxIterations
	}

	for i := 0; i < maxIterations; i++ {
		resp, err := client.CreateChatCompletion(ctx, req)
		if err != nil {
			return "", err
		}

		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("chat completion returned no choices")
		}

		choice := resp.Choices[0]
		if choice.FinishReason != openai.FinishReasonToolCalls {
			return choice.Message.Content, nil
		}

		if len(choice.Message.ToolCalls) == 0 {
			return "", fmt.Errorf("chat completion finished with tool calls but returned none")
		}

		req.Messages = append(req.Messages, choice.Message)
		for _, toolCall := range choice.Message.ToolCalls {
			log.Printf("[tools] calling %s args=%s", toolCall.Function.Name, toolCall.Function.Arguments)
			toolResult := registry.Execute(ctx, toolCall)
			req.Messages = append(req.Messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: toolCall.ID,
				Content:    toolResult,
				Name:       toolCall.Function.Name,
			})
		}
	}

	return "", fmt.Errorf("tool loop exceeded maximum iterations (%d)", maxIterations)
}
