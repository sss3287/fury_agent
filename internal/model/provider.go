package model

import "context"

// Provider is the narrow common interface that powers the agent loop. The core
// loop never knows which concrete provider was compiled in (GO_AGENT_PLAN.md
// §3.3, §5). Native returns the complete provider escape hatch.
type Provider interface {
	// Identity returns the compile-time provider identity.
	Identity() ProviderIdentity

	// Capabilities reports advisory feature support. It may perform discovery
	// or return a static table; it must not gate the native client.
	Capabilities(ctx context.Context) (Capabilities, error)

	// Generate performs a single non-streaming model turn.
	Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error)

	// Stream performs a single streaming model turn. Callers must Close the
	// returned stream.
	Stream(ctx context.Context, req GenerateRequest) (EventStream, error)

	// CountTokens counts tokens for a request where the provider supports it.
	CountTokens(ctx context.Context, req TokenCountRequest) (TokenCountResponse, error)

	// Native exposes the provider-native escape hatch.
	Native() NativeClient
}
