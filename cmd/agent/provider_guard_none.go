//go:build !provider_fake && !provider_openai && !provider_anthropic && !provider_gemini && !provider_router

package main

// This file is compiled only when NO provider build tag is selected. It
// references an undefined identifier so the build fails with a message telling
// the operator to select exactly one provider (GO_AGENT_PLAN.md §3.1).
func init() {
	_ = BUILD_ERROR_select_one_provider_tag_provider_anthropic_or_provider_openai_or_provider_gemini_or_provider_fake
}
