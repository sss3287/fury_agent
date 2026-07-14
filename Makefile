# fury_agent build automation.
#
# Each binary links exactly one provider via a provider_* build tag, with the
# provider and default model embedded at build time (GO_AGENT_PLAN.md §3.1, §12).

MODULE   := github.com/ahhh/fury_agent
BININFO  := $(MODULE)/internal/buildinfo
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
VERSION  ?= dev
NOW      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
DIST     := dist

# Default models per provider (override on the command line, e.g. MODEL=...).
MODEL_anthropic ?= claude-opus-4-8
MODEL_openai    ?= gpt-4.1
MODEL_gemini    ?= gemini-2.5-pro
MODEL_fake      ?= fake-1

GOFLAGS  := -trimpath
CGO      := CGO_ENABLED=0

define LDFLAGS
-s -w \
 -X $(BININFO).Provider=$(1) \
 -X $(BININFO).Model=$(2) \
 -X $(BININFO).ProviderTag=provider_$(1) \
 -X $(BININFO).Version=$(VERSION) \
 -X $(BININFO).Commit=$(COMMIT) \
 -X $(BININFO).Time=$(NOW)
endef

.PHONY: all build-anthropic build-openai build-gemini build-fake test test-contract vet fmt doctor clean

all: build-anthropic build-openai build-gemini

build-anthropic:
	$(CGO) go build $(GOFLAGS) -tags provider_anthropic \
	  -ldflags "$(call LDFLAGS,anthropic,$(MODEL_anthropic))" \
	  -o $(DIST)/agent-anthropic ./cmd/agent

build-openai:
	$(CGO) go build $(GOFLAGS) -tags provider_openai \
	  -ldflags "$(call LDFLAGS,openai,$(MODEL_openai))" \
	  -o $(DIST)/agent-openai ./cmd/agent

build-gemini:
	$(CGO) go build $(GOFLAGS) -tags provider_gemini \
	  -ldflags "$(call LDFLAGS,gemini,$(MODEL_gemini))" \
	  -o $(DIST)/agent-gemini ./cmd/agent

build-fake:
	$(CGO) go build $(GOFLAGS) -tags provider_fake \
	  -ldflags "$(call LDFLAGS,fake,$(MODEL_fake))" \
	  -o $(DIST)/agent-fake ./cmd/agent

# A provider tag is required because the build-tag guard fails a no-provider build.
test:
	go test -tags provider_fake ./...

# Contract tests exercise the provider adapters against local fake servers.
test-contract:
	go test ./internal/provider/...

vet:
	go vet -tags provider_fake ./...

fmt:
	gofmt -l -w .

doctor: build-fake
	./$(DIST)/agent-fake doctor

clean:
	rm -rf $(DIST)
