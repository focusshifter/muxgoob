package tools

import (
	"context"
	"fmt"
	"log"
	"strings"

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

type sentActionTool interface {
	WasSent() bool
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
		result := toolError(toolCall.Function.Name, "tool registry is nil")
		log.Printf("[tools] %s failed %s", toolCall.Function.Name, summarizeToolResult(result))
		return result
	}

	tool, ok := r.tools[toolCall.Function.Name]
	if !ok {
		result := toolError(toolCall.Function.Name, "unknown tool")
		log.Printf("[tools] %s failed %s", toolCall.Function.Name, summarizeToolResult(result))
		return result
	}

	result, err := tool.Execute(ctx, toolCall.Function.Arguments)
	if err != nil {
		toolResult := toolError(toolCall.Function.Name, err.Error())
		log.Printf("[tools] %s failed %s", toolCall.Function.Name, summarizeToolResult(toolResult))
		return toolResult
	}

	log.Printf("[tools] %s returned %s", toolCall.Function.Name, summarizeToolResult(result))

	return result
}

func (r *Registry) toolSentAction(name string) bool {
	if r == nil {
		return false
	}
	tool, ok := r.tools[name]
	if !ok {
		return false
	}
	actionTool, ok := tool.(sentActionTool)
	return ok && actionTool.WasSent()
}

func formatToolCallNames(toolCalls []openai.ToolCall) string {
	if len(toolCalls) == 0 {
		return ""
	}
	parts := make([]string, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		name := toolCall.Function.Name
		if name == "" {
			name = "<unnamed>"
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, ",")
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
		log.Printf("[tools] completion request iteration=%d model=%s messages=%d tools=%d", i+1, req.Model, len(req.Messages), len(req.Tools))
		resp, err := client.CreateChatCompletion(ctx, req)
		if err != nil {
			log.Printf("[tools] completion request failed iteration=%d model=%s err=%v", i+1, req.Model, err)
			return "", err
		}

		if len(resp.Choices) == 0 {
			log.Printf("[tools] completion returned no choices iteration=%d model=%s", i+1, req.Model)
			return "", fmt.Errorf("chat completion returned no choices")
		}

		choice := resp.Choices[0]
		if choice.FinishReason != openai.FinishReasonToolCalls {
			if choice.Message.Content == "" {
				log.Printf("[tools] completion finished reason=%s with empty content", choice.FinishReason)
			} else {
				log.Printf("[tools] completion finished reason=%s content_len=%d", choice.FinishReason, len(choice.Message.Content))
			}
			return choice.Message.Content, nil
		}

		if len(choice.Message.ToolCalls) == 0 {
			return "", fmt.Errorf("chat completion finished with tool calls but returned none")
		}
		log.Printf("[tools] completion returned tool_calls count=%d names=%s", len(choice.Message.ToolCalls), formatToolCallNames(choice.Message.ToolCalls))

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
			if registry.toolSentAction(toolCall.Function.Name) {
				log.Printf("[tools] %s sent action result, ending tool loop without follow-up completion", toolCall.Function.Name)
				return "", nil
			}
		}
		if forcedChoice, ok := req.ToolChoice.(openai.ToolChoice); ok && forcedChoice.Type == openai.ToolTypeFunction {
			req.ToolChoice = nil
		}
	}

	return "", fmt.Errorf("tool loop exceeded maximum iterations (%d)", maxIterations)
}
