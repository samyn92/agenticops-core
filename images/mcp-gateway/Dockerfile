# Build the mcp-gateway binary
FROM golang:1.25 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Copy module definition (stdlib-only, no external deps)
COPY go.mod ./

# Copy source
COPY main.go main.go
COPY internal/ internal/

# Build static binary
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o mcp-gateway .

# ---
# Proxy mode: distroless is fine (just HTTP proxying).
# Spawn mode: the operator should use the MCPServer's spec.image
# as the container image and copy the gateway binary via an init
# container. For now, this image works as a proxy sidecar and for
# MCP servers that only need the gateway binary itself.
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/mcp-gateway .
USER 65532:65532

ENTRYPOINT ["/mcp-gateway"]
