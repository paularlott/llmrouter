package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/paularlott/mcp/ai"
)

// supportsDecision reports whether the provider's client can run decision
// (System One) models. Currently only Ollama providers can.
func supportsDecision(p *Provider) bool {
	if p == nil || p.Client == nil {
		return false
	}
	_, ok := p.Client.(ai.DecisionCaller)
	return ok
}

// Decide routes a System One decision request to a provider that serves the
// requested model and supports decision models.
func (r *Router) Decide(ctx context.Context, req *ai.SystemOneRequest) (*ai.SystemOneResponse, error) {
	providerName, err := r.getProviderForModelFiltered(req.Model, "", supportsDecision)
	if err != nil {
		if strings.Contains(err.Error(), "no provider supporting") {
			err = fmt.Errorf("%w (decision models need a provider of type \"ollama\")", err)
		}
		return nil, err
	}

	provider := r.Providers[providerName]
	r.logger.Info("routing decision request", "model", req.Model, "provider", providerName)

	dispatchReq := *req
	dispatchReq.Model = r.resolveAliasForProvider(req.Model, providerName)

	var watchReqID string
	if r.requestWatcher.Active() {
		watchReqID = r.requestWatcher.NewRequestID()
		r.requestWatcher.EmitRequest(watchReqID, "systemone", providerName, dispatchReq.Model, &dispatchReq)
	}

	r.incrementActiveCompletions(providerName)
	defer r.decrementActiveCompletions(providerName)
	r.recordModelUse(providerName, req.Model)

	resp, err := provider.Client.(ai.DecisionCaller).Decide(ctx, dispatchReq)
	if err != nil {
		if watchReqID != "" && r.requestWatcher.Active() {
			r.requestWatcher.EmitError(watchReqID, "systemone", providerName, dispatchReq.Model, err)
		}
		if r.isConnectionError(err) {
			r.DisableProvider(providerName, fmt.Sprintf("connection error: %v", err))
		}
		return nil, err
	}

	if watchReqID != "" && r.requestWatcher.Active() {
		r.requestWatcher.EmitResponse(watchReqID, "systemone", providerName, dispatchReq.Model, resp)
	}
	return resp, nil
}

// HandleSystemOne serves POST /v1/systemone, the Ollama decision model API.
func (r *Router) HandleSystemOne(w http.ResponseWriter, req *http.Request) {
	var decReq ai.SystemOneRequest
	if err := readJSON(req, &decReq); err != nil {
		r.logger.WithError(err).Error("failed to parse systemone request")
		writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if decReq.Model == "" {
		writeJSONError(w, http.StatusBadRequest, "model is required")
		return
	}

	resp, err := r.Decide(req.Context(), &decReq)
	if err != nil {
		r.logger.WithError(err).Error("systemone request failed")
		status := http.StatusInternalServerError
		switch {
		case strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no provider supporting"):
			status = http.StatusNotFound
		case strings.Contains(err.Error(), "status 400"):
			// The provider rejected the request itself (e.g. the model is not a decision model).
			status = http.StatusBadRequest
		}
		writeJSONError(w, status, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := writeJSON(w, resp); err != nil {
		r.logger.WithError(err).Error("failed to write systemone response")
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
