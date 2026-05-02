# Build the token-injector binary
FROM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Copy module definition (stdlib-only, no external deps)
COPY go.mod ./

# Copy source
COPY main.go main.go

# Build static binary
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o token-injector .

# ---
# OAuth2 client_credentials reverse proxy.
# Sits in front of an LLM provider API, exchanges client_id/client_secret
# for a bearer token at TOKEN_URL, caches it, and forwards every request
# to TARGET_URL with Authorization: Bearer <token>.
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/token-injector .
USER 65532:65532

ENTRYPOINT ["/token-injector"]
