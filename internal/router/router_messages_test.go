package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/paularlott/llmrouter/internal/types"
	"github.com/paularlott/mcp/ai/claude"
	"github.com/paularlott/mcp/ai/openai"
)

// mockProviderClient wraps a mock response
type mockProviderClient struct {
	Response *openai.ChatCompletionResponse
	Err      error
	LastReq  openai.ChatCompletionRequest
}

func (m *mockProviderClient) ChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (*openai.ChatCompletionResponse, error) {
	m.LastReq = req
	return m.Response, m.Err
}
func (m *mockProviderClient) StreamChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) *openai.ChatStream {
	return nil
}
func (m *mockProviderClient) Provider() string                   { return "mock" }
func (m *mockProviderClient) SupportsCapability(cap string) bool { return true }
func (m *mockProviderClient) GetModels(ctx context.Context) (*openai.ModelsResponse, error) {
	return nil, nil
}
func (m *mockProviderClient) CreateEmbedding(ctx context.Context, req openai.EmbeddingRequest) (*openai.EmbeddingResponse, error) {
	return nil, nil
}
func (m *mockProviderClient) StreamResponse(ctx context.Context, req openai.CreateResponseRequest) *openai.ResponseStream {
	return nil
}
func (m *mockProviderClient) CreateResponse(ctx context.Context, req openai.CreateResponseRequest) (*openai.ResponseObject, error) {
	return nil, nil
}
func (m *mockProviderClient) GetResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return nil, nil
}
func (m *mockProviderClient) CancelResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return nil, nil
}
func (m *mockProviderClient) DeleteResponse(ctx context.Context, id string) error { return nil }
func (m *mockProviderClient) CompactResponse(ctx context.Context, id string) (*openai.ResponseObject, error) {
	return nil, nil
}
func (m *mockProviderClient) Close() error { return nil }

func TestHandleMessages_Conversion(t *testing.T) {
	mockClient := &mockProviderClient{
		Response: &openai.ChatCompletionResponse{
			ID:    "msg_123",
			Model: "mock-model",
			Choices: []openai.Choice{
				{
					Message: openai.Message{
						Content: "Mock response text",
					},
					FinishReason: "stop",
				},
			},
		},
	}

	router := &Router{
		Providers: map[string]*Provider{
			"mock-provider": {
				Name:         "mock-provider",
				ProviderType: "openai",
				Enabled:      true,
				Healthy:      true,
				Models:       []string{"mock-model"},
				Client:       mockClient,
			},
		},
		ModelMap: map[string][]string{
			"mock-model": {"mock-provider"},
		},
		logger: &testLogger{},
		config: &types.Config{},
	}

	reqBody := map[string]interface{}{
		"model": "mock-model",
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": "Hello"},
				},
			},
		},
	}
	reqData, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(reqData))
	rec := httptest.NewRecorder()

	router.HandleMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	if len(mockClient.LastReq.Messages) != 1 {
		t.Fatalf("Expected 1 message to upstream, got %d", len(mockClient.LastReq.Messages))
	}
	if mockClient.LastReq.Messages[0].Content != "Hello" {
		t.Fatalf("Expected upstream message content 'Hello', got '%v'", mockClient.LastReq.Messages[0].Content)
	}

	var resp claude.MessagesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.ID != "msg_123" {
		t.Fatalf("Expected msg_123, got %s", resp.ID)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(resp.Content))
	}
	if resp.Content[0].Text != "Mock response text" {
		t.Fatalf("Expected 'Mock response text', got %s", resp.Content[0].Text)
	}
}

func TestHandleChatCompletions_PreservesProviderExtraBody(t *testing.T) {
	mockClient := &mockProviderClient{
		Response: &openai.ChatCompletionResponse{
			ID:    "chatcmpl_123",
			Model: "mock-model",
			Choices: []openai.Choice{
				{
					Message: openai.Message{
						Role:    "assistant",
						Content: "ok",
					},
					FinishReason: "stop",
				},
			},
		},
	}

	router := &Router{
		Providers: map[string]*Provider{
			"mock-provider": {
				Name:         "mock-provider",
				ProviderType: "openai",
				Enabled:      true,
				Healthy:      true,
				Models:       []string{"mock-model"},
				Client:       mockClient,
			},
		},
		ModelMap: map[string][]string{
			"mock-model": {"mock-provider"},
		},
		logger: &testLogger{},
		config: &types.Config{},
	}

	reqBody := map[string]interface{}{
		"model": "mock-model",
		"messages": []map[string]interface{}{
			{"role": "user", "content": "Hello"},
		},
		"thinking": map[string]interface{}{
			"type": "enabled",
		},
		"extra_body": map[string]interface{}{
			"custom_flag": true,
		},
	}
	reqData, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(reqData))
	rec := httptest.NewRecorder()

	router.HandleChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	thinking, ok := mockClient.LastReq.ExtraBody["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("Expected thinking in forwarded ExtraBody, got %#v", mockClient.LastReq.ExtraBody)
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("Expected forwarded thinking.type enabled, got %#v", thinking["type"])
	}
	if mockClient.LastReq.ExtraBody["custom_flag"] != true {
		t.Fatalf("Expected custom_flag in forwarded ExtraBody, got %#v", mockClient.LastReq.ExtraBody)
	}
}

func TestHandleMessages_ToolConversion(t *testing.T) {
	mockClient := &mockProviderClient{
		Response: &openai.ChatCompletionResponse{
			ID:    "msg_456",
			Model: "mock-model",
			Choices: []openai.Choice{
				{
					Message: openai.Message{
						Role: "assistant",
						ToolCalls: []openai.ToolCall{
							{
								ID:   "call_789",
								Type: "function",
								Function: openai.ToolCallFunction{
									Name:      "get_weather",
									Arguments: map[string]any{"location": "London"},
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			},
		},
	}

	router := &Router{
		Providers: map[string]*Provider{
			"mock-provider": {
				Name:         "mock-provider",
				ProviderType: "openai",
				Enabled:      true,
				Healthy:      true,
				Models:       []string{"mock-model"},
				Client:       mockClient,
			},
		},
		ModelMap: map[string][]string{
			"mock-model": {"mock-provider"},
		},
		logger: &testLogger{},
		config: &types.Config{},
	}

	reqBody := map[string]interface{}{
		"model": "mock-model",
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{"type": "text", "text": "What's the weather in London?"},
				},
			},
		},
		"tools": []map[string]interface{}{
			{
				"name":        "get_weather",
				"description": "Get current weather in a given location",
				"input_schema": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"location": map[string]interface{}{
							"type": "string",
						},
					},
					"required": []string{"location"},
				},
			},
		},
	}
	reqData, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(reqData))
	rec := httptest.NewRecorder()

	router.HandleMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	if len(mockClient.LastReq.Tools) != 1 {
		t.Fatalf("Expected 1 tool to upstream, got %d", len(mockClient.LastReq.Tools))
	}
	if mockClient.LastReq.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("Expected upstream tool 'get_weather', got '%s'", mockClient.LastReq.Tools[0].Function.Name)
	}

	var resp claude.MessagesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.StopReason != "tool_use" {
		t.Fatalf("Expected 'tool_use' StopReason, got %v", resp.StopReason)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(resp.Content))
	}
	if resp.Content[0].Type != "tool_use" {
		t.Fatalf("Expected type 'tool_use', got %v", resp.Content[0].Type)
	}
	if resp.Content[0].Name != "get_weather" {
		t.Fatalf("Expected tool name 'get_weather', got %v", resp.Content[0].Name)
	}
	if resp.Content[0].ID != "call_789" {
		t.Fatalf("Expected tool ID 'call_789', got %v", resp.Content[0].ID)
	}
}

func TestHandleMessages_ToolResultConversion(t *testing.T) {
	// A mock response validating that a tool_result from Claude was translated securely into a role:tool openai message
	mockClient := &mockProviderClient{
		Response: &openai.ChatCompletionResponse{
			ID:    "msg_999",
			Model: "mock-model",
			Choices: []openai.Choice{
				{
					Message: openai.Message{
						Role:    "assistant",
						Content: "The weather in London is sunny.",
					},
					FinishReason: "stop",
				},
			},
		},
	}

	router := &Router{
		Providers: map[string]*Provider{
			"mock-provider": {
				Name:         "mock-provider",
				ProviderType: "openai",
				Enabled:      true,
				Healthy:      true,
				Models:       []string{"mock-model"},
				Client:       mockClient,
			},
		},
		ModelMap: map[string][]string{
			"mock-model": {"mock-provider"},
		},
		logger: &testLogger{},
		config: &types.Config{},
	}

	// Prepare Claude API Request: User submits a tool_result
	reqBody := map[string]interface{}{
		"model": "mock-model",
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type":        "tool_result",
						"tool_use_id": "call_789",
						"content":     "Sunny and 20 degrees",
					},
				},
			},
		},
	}
	reqData, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(reqData))
	rec := httptest.NewRecorder()

	router.HandleMessages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	// 1) Verify the mocked upstream received the converted tool_result message
	if len(mockClient.LastReq.Messages) != 1 {
		t.Fatalf("Expected 1 message to upstream, got %d", len(mockClient.LastReq.Messages))
	}
	if mockClient.LastReq.Messages[0].Role != "tool" {
		t.Fatalf("Expected upstream message to be 'tool', got '%s'", mockClient.LastReq.Messages[0].Role)
	}
	if mockClient.LastReq.Messages[0].ToolCallID != "call_789" {
		t.Fatalf("Expected upstream tool_call_id to be 'call_789', got '%s'", mockClient.LastReq.Messages[0].ToolCallID)
	}
	if mockClient.LastReq.Messages[0].Content != "Sunny and 20 degrees" {
		t.Fatalf("Expected upstream tool content to be 'Sunny and 20 degrees', got '%v'", mockClient.LastReq.Messages[0].Content)
	}

	// 2) Verify the router returned a converted Claude response
	var resp claude.MessagesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.StopReason != "end_turn" { // mapped from 'stop'
		t.Fatalf("Expected 'end_turn' StopReason, got %v", resp.StopReason)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(resp.Content))
	}
	if resp.Content[0].Type != "text" {
		t.Fatalf("Expected type 'text', got %v", resp.Content[0].Type)
	}
	if resp.Content[0].Text != "The weather in London is sunny." {
		t.Fatalf("Expected text 'The weather in London is sunny.', got %v", resp.Content[0].Text)
	}
}
