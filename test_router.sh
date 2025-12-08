#!/bin/bash

# Simple test script for the LLM Router

echo "Starting LLM Router test..."

# Check if the binary exists
if [ ! -f "./llmrouter" ]; then
    echo "Building the router..."
    go build -o llmrouter .
    if [ $? -ne 0 ]; then
        echo "Build failed!"
        exit 1
    fi
fi

# Test health endpoint
echo "Testing health endpoint..."
curl -s http://localhost:12345/health | jq .

# Test models endpoint (this will likely fail without real API keys, but should show the structure)
echo "Testing models endpoint..."
curl -s http://localhost:12345/v1/models | jq .

# Test a simple chat completion (will fail without API keys, but shows the request routing)
echo "Testing chat completion endpoint..."
curl -s -X POST http://localhost:12345/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "google/gemma-3-1b",
    "messages": [
      {"role": "user", "content": "Hello, world!"}
    ],
    "max_tokens": 10
  }' | jq .

echo "Test complete!"