package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

type OpenAIClientImpl struct {
	BaseURL string
	Token   string
	Client  *http.Client
	logger  Logger
}

func NewOpenAIClient(baseURL, token string, logger Logger) *OpenAIClientImpl {
	// Configure HTTP transport with HTTP/2 support and connection pooling
	transport := &http.Transport{
		// Connection pooling settings
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,

		// Enable HTTP/2
		ForceAttemptHTTP2: true,
	}

	// Set up HTTP/2 configuration
	err := http2.ConfigureTransport(transport)
	if err != nil {
		// If HTTP/2 configuration fails, we'll still use the transport with HTTP/1.1
		logger.Warn("failed to configure HTTP/2 transport", "error", err)
	}

	return &OpenAIClientImpl{
		BaseURL: baseURL,
		Token:   token,
		Client: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		logger: logger,
	}
}

func (c *OpenAIClientImpl) ListModels(ctx context.Context) (*ModelsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to parse as error response
		var errResp map[string]interface{}
		if json.Unmarshal(body, &errResp) == nil {
			return nil, fmt.Errorf("API returned status %d: %v", resp.StatusCode, errResp)
		}
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var modelsResp ModelsResponse
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		// Log the actual response for debugging
		maxLen := 500
		if len(body) < maxLen {
			maxLen = len(body)
		}
		c.logger.Error("failed to decode models response",
			"error", err,
			"status_code", resp.StatusCode,
			"content_type", resp.Header.Get("Content-Type"),
			"response_body", string(body[:maxLen])) // Log first 500 chars
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	c.logger.Debug("listed models from provider", "count", len(modelsResp.Data), "base_url", c.BaseURL)
	return &modelsResp, nil
}

// ListModelsWithTimeout fetches models with a 5-second timeout
func (c *OpenAIClientImpl) ListModelsWithTimeout(ctx context.Context) (*ModelsResponse, error) {
	// Create a context with 5-second timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return c.ListModels(timeoutCtx)
}

func (c *OpenAIClientImpl) CreateChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Check if this is a streaming response
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		// Return error - this method should not be called for streaming requests
		return nil, fmt.Errorf("streaming request received, use CreateChatCompletionRaw instead")
	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to parse as error response
		var errResp map[string]interface{}
		if json.Unmarshal(body, &errResp) == nil {
			return nil, fmt.Errorf("API returned status %d: %v", resp.StatusCode, errResp)
		}
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var completionResp ChatCompletionResponse
	if err := json.Unmarshal(body, &completionResp); err != nil {
		// Log the actual response for debugging
		maxLen := 500
		if len(body) < maxLen {
			maxLen = len(body)
		}
		c.logger.Error("failed to decode chat completion response",
			"error", err,
			"status_code", resp.StatusCode,
			"content_type", resp.Header.Get("Content-Type"),
			"response_body", string(body[:maxLen])) // Log first 500 chars
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	c.logger.Debug("chat completion completed", "model", req.Model, "response_id", completionResp.ID)
	return &completionResp, nil
}

func (c *OpenAIClientImpl) CreateChatCompletionRaw(ctx context.Context, req *ChatCompletionRequest) (*http.Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}

	return resp, nil
}

func (c *OpenAIClientImpl) CreateEmbedding(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.Token)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("failed to read response body: %w", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		if json.Unmarshal(body, &errResp) == nil {
			return nil, fmt.Errorf("API returned status %d: %v", resp.StatusCode, errResp)
		}
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var embeddingResp EmbeddingResponse
	if err := json.Unmarshal(body, &embeddingResp); err != nil {
		maxLen := 500
		if len(body) < maxLen {
			maxLen = len(body)
		}
		c.logger.Error("failed to decode embedding response",
			"error", err,
			"status_code", resp.StatusCode,
			"content_type", resp.Header.Get("Content-Type"),
			"response_body", string(body[:maxLen]))
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	c.logger.Debug("embedding completed", "model", req.Model, "embeddings_count", len(embeddingResp.Data))
	return &embeddingResp, nil
}
