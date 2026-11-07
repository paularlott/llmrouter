package router

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/paularlott/llmrouter/internal/types"
	mcplib "github.com/paularlott/mcp"
)

// mcpResourceName is the human-readable name published in the MCP endpoint's
// protected resource metadata (RFC 9728).
const mcpResourceName = "LLM Router"

// mcpResourceURL is the canonical resource identifier (RFC 8707) of the /mcp
// endpoint: <public_url>/mcp when a public URL is configured, else derived
// from the bind address. A wildcard or empty bind host becomes "localhost",
// since it is not an address a client can reach.
func mcpResourceURL(cfg types.ServerConfig) string {
	if cfg.PublicURL != "" {
		return strings.TrimSuffix(cfg.PublicURL, "/") + "/mcp"
	}
	host := cfg.Host
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "localhost"
	}
	host = strings.Trim(host, "[]")
	return "http://" + strings.ToLower(net.JoinHostPort(host, strconv.Itoa(cfg.Port))) + "/mcp"
}

// newMCPResourceServer builds the OAuth 2.1 resource server guarding /mcp.
// It returns nil when neither a static token nor an OIDC issuer is
// configured (the endpoint then stays unauthenticated, as before).
//
// A static server token is accepted as a bearer token (constant-time
// compare); an OIDC issuer's JWT access tokens are accepted when an issuer
// is configured. With both configured either is accepted.
func newMCPResourceServer(cfg types.ServerConfig) (*mcplib.ResourceServer, error) {
	oidc := cfg.OIDC
	if cfg.Token == "" && oidc.Issuer == "" {
		return nil, nil
	}

	rs := &mcplib.ResourceServer{
		Resource:     mcpResourceURL(cfg),
		ResourceName: mcpResourceName,
	}

	var static, jwt mcplib.TokenValidator
	if cfg.Token != "" {
		// The static token is this server's own secret: it carries no
		// audience (accepted as this resource) and is granted the required
		// scopes so RequiredScopes only constrains identity-provider tokens.
		static = mcplib.StaticTokenValidator(map[string]*mcplib.TokenInfo{
			cfg.Token: {Subject: "static-token", Scopes: append([]string(nil), oidc.RequiredScopes...)},
		})
	}
	if oidc.Issuer != "" {
		v, err := mcplib.NewJWTValidator(mcplib.JWTValidatorConfig{
			Issuer:                 oidc.Issuer,
			JWKSURL:                oidc.JWKSURL,
			RequireAccessTokenType: oidc.RequireAccessTokenType,
		})
		if err != nil {
			return nil, fmt.Errorf("OIDC token validator: %w", err)
		}
		jwt = v.Validate
		rs.AuthorizationServers = []string{oidc.Issuer}
		rs.RequiredScopes = oidc.RequiredScopes
		rs.ScopesSupported = oidc.RequiredScopes
		rs.Audiences = oidc.Audience
	}

	switch {
	case static != nil && jwt != nil:
		rs.ValidateToken = func(r *http.Request, token string) (*mcplib.TokenInfo, error) {
			if info, err := static(r, token); err == nil {
				return info, nil
			}
			return jwt(r, token)
		}
	case static != nil:
		rs.ValidateToken = static
	default:
		rs.ValidateToken = jwt
	}
	return rs, nil
}
