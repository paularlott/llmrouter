package router

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/paularlott/mcp/ai/openai"
)

const ollamaAPIVersion = "0.23.0"

var ollamaModelCapabilities = []string{"completion", "tools", "vision"}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content,omitempty"`
	Images    []string         `json:"images,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaToolCall struct {
	Function ollamaToolCallFunction `json:"function"`
}

type ollamaToolCallFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type ollamaTool struct {
	Type     string              `json:"type"`
	Function openai.ToolFunction `json:"function"`
}

type ollamaOptions map[string]any

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Tools    []ollamaTool    `json:"tools,omitempty"`
	Format   any             `json:"format,omitempty"`
	Options  ollamaOptions   `json:"options,omitempty"`
	Stream   *bool           `json:"stream,omitempty"`
}

type ollamaGenerateRequest struct {
	Model   string        `json:"model"`
	Prompt  string        `json:"prompt"`
	Suffix  string        `json:"suffix,omitempty"`
	System  string        `json:"system,omitempty"`
	Images  []string      `json:"images,omitempty"`
	Format  any           `json:"format,omitempty"`
	Options ollamaOptions `json:"options,omitempty"`
	Stream  *bool         `json:"stream,omitempty"`
	Raw     bool          `json:"raw,omitempty"`
}

type ollamaChatResponse struct {
	Model              string        `json:"model"`
	CreatedAt          string        `json:"created_at"`
	Message            ollamaMessage `json:"message,omitempty"`
	DoneReason         string        `json:"done_reason,omitempty"`
	Done               bool          `json:"done"`
	TotalDuration      int64         `json:"total_duration,omitempty"`
	LoadDuration       int64         `json:"load_duration,omitempty"`
	PromptEvalCount    int           `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64         `json:"prompt_eval_duration,omitempty"`
	EvalCount          int           `json:"eval_count,omitempty"`
	EvalDuration       int64         `json:"eval_duration,omitempty"`
}

type ollamaGenerateResponse struct {
	Model              string `json:"model"`
	CreatedAt          string `json:"created_at"`
	Response           string `json:"response"`
	DoneReason         string `json:"done_reason,omitempty"`
	Done               bool   `json:"done"`
	TotalDuration      int64  `json:"total_duration,omitempty"`
	LoadDuration       int64  `json:"load_duration,omitempty"`
	PromptEvalCount    int    `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64  `json:"prompt_eval_duration,omitempty"`
	EvalCount          int    `json:"eval_count,omitempty"`
	EvalDuration       int64  `json:"eval_duration,omitempty"`
}

type ollamaEmbedRequest struct {
	Model      string `json:"model"`
	Input      any    `json:"input"`
	Dimensions int    `json:"dimensions,omitempty"`
}

type ollamaEmbeddingsRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

func (r *Router) HandleOllamaVersion(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, map[string]string{"version": ollamaAPIVersion}); err != nil {
		r.logger.WithError(err).Error("failed to write ollama version response")
	}
}

func (r *Router) HandleOllamaTags(w http.ResponseWriter, req *http.Request) {
	r.RefreshModels(req.Context())
	now := time.Now().UTC().Format(time.RFC3339Nano)
	modelsResp := r.ListModels()
	models := make([]map[string]any, 0, len(modelsResp.Data))
	for _, model := range modelsResp.Data {
		models = append(models, map[string]any{
			"name":         model.ID,
			"model":        model.ID,
			"modified_at":  now,
			"size":         0,
			"digest":       "",
			"capabilities": ollamaCapabilities(),
			"details": map[string]any{
				"format":             "router",
				"family":             "router",
				"families":           []string{"router"},
				"parameter_size":     "",
				"quantization_level": "",
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, map[string]any{"models": models}); err != nil {
		r.logger.WithError(err).Error("failed to write ollama tags response")
	}
}

func (r *Router) HandleOllamaRunningModels(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, map[string]any{"models": []any{}}); err != nil {
		r.logger.WithError(err).Error("failed to write ollama ps response")
	}
}

func (r *Router) HandleOllamaShow(w http.ResponseWriter, req *http.Request) {
	var showReq struct {
		Model string `json:"model"`
	}
	if err := readJSON(req, &showReq); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if showReq.Model == "" {
		http.Error(w, "model is required", http.StatusBadRequest)
		return
	}
	r.ModelMapMu.RLock()
	_, ok := r.ModelMap[showReq.Model]
	r.ModelMapMu.RUnlock()
	if !ok && r.smartRouterFor(showReq.Model) == nil {
		http.Error(w, fmt.Sprintf("model %s not found", showReq.Model), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, map[string]any{
		"modelfile":    "FROM " + showReq.Model,
		"parameters":   "",
		"template":     "",
		"details":      map[string]any{"family": "router", "families": []string{"router"}, "format": "router"},
		"model_info":   map[string]any{},
		"capabilities": ollamaCapabilities(),
	}); err != nil {
		r.logger.WithError(err).Error("failed to write ollama show response")
	}
}

func (r *Router) HandleOllamaChat(w http.ResponseWriter, req *http.Request) {
	var chatReq ollamaChatRequest
	if err := readJSON(req, &chatReq); err != nil {
		r.logger.WithError(err).Error("failed to parse ollama chat request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	openaiReq := ChatCompletionRequest{
		Model:    chatReq.Model,
		Messages: ollamaMessagesToOpenAI(chatReq.Messages),
		Tools:    ollamaToolsToOpenAI(chatReq.Tools),
		Stream:   ollamaShouldStream(chatReq.Stream),
	}
	applyOllamaOptions(&openaiReq, chatReq.Options)

	if openaiReq.Stream {
		r.handleOllamaChatStream(w, req, &openaiReq)
		return
	}

	start := time.Now()
	resp, err := r.CreateChatCompletion(req.Context(), &openaiReq)
	if err != nil {
		r.writeOllamaError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, ollamaChatFromOpenAI(resp, start)); err != nil {
		r.logger.WithError(err).Error("failed to write ollama chat response")
	}
}

func (r *Router) HandleOllamaGenerate(w http.ResponseWriter, req *http.Request) {
	var genReq ollamaGenerateRequest
	if err := readJSON(req, &genReq); err != nil {
		r.logger.WithError(err).Error("failed to parse ollama generate request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	messages := make([]Message, 0, 2)
	if genReq.System != "" && !genReq.Raw {
		messages = append(messages, Message{Role: "system", Content: genReq.System})
	}
	user := ollamaContentToOpenAI(genReq.Prompt, genReq.Images)
	if genReq.Suffix != "" {
		user = ollamaContentToOpenAI(genReq.Prompt+genReq.Suffix, genReq.Images)
	}
	messages = append(messages, Message{Role: "user", Content: user})

	openaiReq := ChatCompletionRequest{
		Model:    genReq.Model,
		Messages: messages,
		Stream:   ollamaShouldStream(genReq.Stream),
	}
	applyOllamaOptions(&openaiReq, genReq.Options)

	if openaiReq.Stream {
		r.handleOllamaGenerateStream(w, req, &openaiReq)
		return
	}

	start := time.Now()
	resp, err := r.CreateChatCompletion(req.Context(), &openaiReq)
	if err != nil {
		r.writeOllamaError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, ollamaGenerateFromOpenAI(resp, start)); err != nil {
		r.logger.WithError(err).Error("failed to write ollama generate response")
	}
}

func (r *Router) HandleOllamaEmbed(w http.ResponseWriter, req *http.Request) {
	var embedReq ollamaEmbedRequest
	if err := readJSON(req, &embedReq); err != nil {
		r.logger.WithError(err).Error("failed to parse ollama embed request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	start := time.Now()
	resp, err := r.CreateEmbedding(req.Context(), &EmbeddingRequest{
		Model:      embedReq.Model,
		Input:      embedReq.Input,
		Dimensions: embedReq.Dimensions,
	})
	if err != nil {
		r.writeOllamaError(w, err)
		return
	}

	embeddings := make([][]float64, 0, len(resp.Data))
	for _, item := range resp.Data {
		embeddings = append(embeddings, item.Embedding)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, map[string]any{
		"model":             resp.Model,
		"embeddings":        embeddings,
		"total_duration":    time.Since(start).Nanoseconds(),
		"load_duration":     int64(0),
		"prompt_eval_count": resp.Usage.PromptTokens,
	}); err != nil {
		r.logger.WithError(err).Error("failed to write ollama embed response")
	}
}

func (r *Router) HandleOllamaEmbeddings(w http.ResponseWriter, req *http.Request) {
	var embedReq ollamaEmbeddingsRequest
	if err := readJSON(req, &embedReq); err != nil {
		r.logger.WithError(err).Error("failed to parse ollama embeddings request")
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	resp, err := r.CreateEmbedding(req.Context(), &EmbeddingRequest{
		Model: embedReq.Model,
		Input: embedReq.Prompt,
	})
	if err != nil {
		r.writeOllamaError(w, err)
		return
	}

	embedding := []float64{}
	if len(resp.Data) > 0 {
		embedding = resp.Data[0].Embedding
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, map[string]any{"embedding": embedding}); err != nil {
		r.logger.WithError(err).Error("failed to write ollama embeddings response")
	}
}

func (r *Router) handleOllamaChatStream(w http.ResponseWriter, req *http.Request, openaiReq *ChatCompletionRequest) {
	providerName, err := r.resolveStreamingProvider(req, openaiReq)
	if err != nil {
		r.writeOllamaError(w, err)
		return
	}
	stream, _ := r.streamChatCompletion(req.Context(), providerName, openaiReq)
	defer r.decrementActiveCompletions(providerName)

	w.Header().Set("Content-Type", "application/x-ndjson")
	r.writeOllamaNDJSON(w, func(enc *json.Encoder, flush func()) {
		doneReason := "stop"
		for stream.Next() {
			chunk := stream.Current()
			content, tools, finish := ollamaChunkDelta(&chunk)
			if finish != "" {
				doneReason = finish
			}
			if content == "" && len(tools) == 0 {
				continue
			}
			_ = enc.Encode(ollamaChatResponse{
				Model:     openaiReq.Model,
				CreatedAt: ollamaCreatedAt(),
				Message:   ollamaMessage{Role: "assistant", Content: content, ToolCalls: tools},
				Done:      false,
			})
			flush()
		}
		if err := stream.Err(); err != nil {
			r.logger.WithError(err).Error("ollama chat stream error")
		}
		_ = enc.Encode(ollamaChatResponse{
			Model:      openaiReq.Model,
			CreatedAt:  ollamaCreatedAt(),
			Message:    ollamaMessage{Role: "assistant"},
			DoneReason: doneReason,
			Done:       true,
		})
	})
}

func (r *Router) handleOllamaGenerateStream(w http.ResponseWriter, req *http.Request, openaiReq *ChatCompletionRequest) {
	providerName, err := r.resolveStreamingProvider(req, openaiReq)
	if err != nil {
		r.writeOllamaError(w, err)
		return
	}
	stream, _ := r.streamChatCompletion(req.Context(), providerName, openaiReq)
	defer r.decrementActiveCompletions(providerName)

	w.Header().Set("Content-Type", "application/x-ndjson")
	r.writeOllamaNDJSON(w, func(enc *json.Encoder, flush func()) {
		doneReason := "stop"
		for stream.Next() {
			chunk := stream.Current()
			content, _, finish := ollamaChunkDelta(&chunk)
			if finish != "" {
				doneReason = finish
			}
			if content == "" {
				continue
			}
			_ = enc.Encode(ollamaGenerateResponse{
				Model:     openaiReq.Model,
				CreatedAt: ollamaCreatedAt(),
				Response:  content,
				Done:      false,
			})
			flush()
		}
		if err := stream.Err(); err != nil {
			r.logger.WithError(err).Error("ollama generate stream error")
		}
		_ = enc.Encode(ollamaGenerateResponse{
			Model:      openaiReq.Model,
			CreatedAt:  ollamaCreatedAt(),
			Response:   "",
			DoneReason: doneReason,
			Done:       true,
		})
	})
}

func (r *Router) resolveStreamingProvider(req *http.Request, openaiReq *ChatCompletionRequest) (string, error) {
	providerHint := ""
	if sr := r.smartRouterFor(openaiReq.Model); sr != nil {
		result := sr.Route(req.Context(), openaiReq)
		if result.Model == "" {
			return "", fmt.Errorf("model %s not found in any provider", openaiReq.Model)
		}
		openaiReq.Model = result.Model
		providerHint = result.ProviderHint
	}
	return r.GetProviderForModel(openaiReq.Model, providerHint)
}

func (r *Router) writeOllamaNDJSON(w http.ResponseWriter, write func(*json.Encoder, func())) {
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	write(enc, flush)
	flush()
}

func (r *Router) writeOllamaError(w http.ResponseWriter, err error) {
	r.logger.WithError(err).Error("ollama request failed")
	status := http.StatusInternalServerError
	if strings.Contains(err.Error(), "not found") {
		status = http.StatusNotFound
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func ollamaShouldStream(stream *bool) bool {
	return stream == nil || *stream
}

func ollamaMessagesToOpenAI(messages []ollamaMessage) []Message {
	out := make([]Message, 0, len(messages))
	for _, msg := range messages {
		out = append(out, Message{
			Role:    msg.Role,
			Content: ollamaContentToOpenAI(msg.Content, msg.Images),
		})
	}
	return out
}

func ollamaContentToOpenAI(text string, images []string) any {
	if len(images) == 0 {
		return text
	}
	parts := make([]openai.ContentPart, 0, len(images)+1)
	if text != "" {
		parts = append(parts, openai.TextContentPart(text))
	}
	for _, image := range images {
		mediaType := detectImageMediaType(image)
		parts = append(parts, openai.ImageBase64ContentPart(image, mediaType, ""))
	}
	return parts
}

// detectImageMediaType detects the image format from base64-encoded data.
func detectImageMediaType(data string) string {
	// Base64 encodes 3 bytes into 4 chars; decode enough for signature detection.
	end := len(data)
	if end > 16 {
		end = 16
	}
	// Pad if necessary for base64 decoding.
	chunk := data[:end]
	if m := len(chunk) % 4; m != 0 {
		chunk += strings.Repeat("=", 4-m)
	}
	b, err := base64.StdEncoding.DecodeString(chunk)
	if err != nil {
		b, err = base64.URLEncoding.DecodeString(chunk)
		if err != nil {
			return "image/png"
		}
	}
	if len(b) < 4 {
		return "image/png"
	}
	// PNG: 89 50 4E 47
	if b[0] == 0x89 && b[1] == 0x50 && b[2] == 0x4E && b[3] == 0x47 {
		return "image/png"
	}
	// JPEG: FF D8 FF
	if b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF {
		return "image/jpeg"
	}
	// GIF: 47 49 46 38
	if b[0] == 0x47 && b[1] == 0x49 && b[2] == 0x46 && b[3] == 0x38 {
		return "image/gif"
	}
	// WebP: 52 49 46 46 ... 57 45 42 50
	if b[0] == 0x52 && b[1] == 0x49 && b[2] == 0x46 && b[3] == 0x46 {
		return "image/webp"
	}
	return "image/png"
}

func ollamaToolsToOpenAI(tools []ollamaTool) []Tool {
	out := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		toolType := tool.Type
		if toolType == "" {
			toolType = "function"
		}
		out = append(out, Tool{Type: toolType, Function: tool.Function})
	}
	return out
}

func applyOllamaOptions(req *ChatCompletionRequest, options ollamaOptions) {
	if options == nil {
		return
	}
	if v, ok := floatOption(options, "temperature"); ok {
		req.Temperature = &v
	}
	if v, ok := floatOption(options, "top_p"); ok {
		req.TopP = &v
	}
	if v, ok := intOption(options, "num_predict"); ok {
		req.MaxCompletionTokens = v
	}
}

func floatOption(options ollamaOptions, key string) (float64, bool) {
	switch v := options[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	default:
		return 0, false
	}
}

func intOption(options ollamaOptions, key string) (int, bool) {
	switch v := options[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

func ollamaChatFromOpenAI(resp *ChatCompletionResponse, start time.Time) ollamaChatResponse {
	message := ollamaMessage{Role: "assistant"}
	finish := "stop"
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		message.Content = choice.Message.GetContentAsString()
		message.ToolCalls = ollamaToolCallsFromOpenAI(choice.Message.ToolCalls)
		if choice.FinishReason != "" {
			finish = choice.FinishReason
		}
	}
	out := ollamaChatResponse{
		Model:         resp.Model,
		CreatedAt:     ollamaCreatedAt(),
		Message:       message,
		DoneReason:    finish,
		Done:          true,
		TotalDuration: time.Since(start).Nanoseconds(),
	}
	addOllamaUsage(&out, resp.Usage)
	return out
}

func ollamaGenerateFromOpenAI(resp *ChatCompletionResponse, start time.Time) ollamaGenerateResponse {
	out := ollamaGenerateResponse{
		Model:         resp.Model,
		CreatedAt:     ollamaCreatedAt(),
		DoneReason:    "stop",
		Done:          true,
		TotalDuration: time.Since(start).Nanoseconds(),
	}
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		out.Response = choice.Message.GetContentAsString()
		if choice.FinishReason != "" {
			out.DoneReason = choice.FinishReason
		}
	}
	if resp.Usage != nil {
		out.PromptEvalCount = resp.Usage.PromptTokens
		out.EvalCount = resp.Usage.CompletionTokens
	}
	return out
}

func addOllamaUsage(out *ollamaChatResponse, usage *Usage) {
	if usage == nil {
		return
	}
	out.PromptEvalCount = usage.PromptTokens
	out.EvalCount = usage.CompletionTokens
}

func ollamaToolCallsFromOpenAI(toolCalls []ToolCall) []ollamaToolCall {
	out := make([]ollamaToolCall, 0, len(toolCalls))
	for _, call := range toolCalls {
		out = append(out, ollamaToolCall{
			Function: ollamaToolCallFunction{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return out
}

func ollamaChunkDelta(chunk *ChatCompletionResponse) (string, []ollamaToolCall, string) {
	if len(chunk.Choices) == 0 {
		return "", nil, ""
	}
	choice := chunk.Choices[0]
	return choice.Delta.Content, ollamaDeltaToolCallsFromOpenAI(choice.Delta.ToolCalls), choice.FinishReason
}

func ollamaDeltaToolCallsFromOpenAI(toolCalls []openai.DeltaToolCall) []ollamaToolCall {
	out := make([]ollamaToolCall, 0, len(toolCalls))
	for _, call := range toolCalls {
		args := map[string]any{}
		if call.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
		}
		out = append(out, ollamaToolCall{
			Function: ollamaToolCallFunction{
				Name:      call.Function.Name,
				Arguments: args,
			},
		})
	}
	return out
}

func ollamaCreatedAt() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func ollamaCapabilities() []string {
	capabilities := make([]string, len(ollamaModelCapabilities))
	copy(capabilities, ollamaModelCapabilities)
	return capabilities
}
