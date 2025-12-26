# Dockerfile for OpenShift Coordination Engine (Go)

# Stage 1: Build the Go binary
FROM registry.access.redhat.com/ubi9/go-toolset:9.7 AS builder

WORKDIR /workspace

# Copy Go module files
COPY go.mod go.sum* ./

# Download dependencies
RUN go mod download

# Copy source code
COPY cmd/ cmd/
COPY internal/ internal/
COPY pkg/ pkg/

# Build the binary with version info
ARG VERSION=dev
ARG BUILD_DATE
ARG VCS_REF
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.Version=${VERSION}" \
    -o bin/coordination-engine \
    cmd/coordination-engine/main.go

# Stage 2: Create minimal runtime image
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

# Image labels
LABEL name="coordination-engine" \
      vendor="OpenShift AIOps" \
      version="${VERSION}" \
      release="1" \
      summary="OpenShift Coordination Engine" \
      description="Go-based coordination engine for multi-layer remediation in OpenShift clusters" \
      io.k8s.description="Orchestrates multi-layer remediation across infrastructure, platform, and application layers" \
      io.k8s.display-name="Coordination Engine" \
      io.openshift.tags="aiops,coordination,remediation,openshift"

# Install ca-certificates for HTTPS calls
RUN microdnf install -y ca-certificates && \
    microdnf clean all

WORKDIR /app

# Copy binary from builder
COPY --from=builder /workspace/bin/coordination-engine /app/coordination-engine

# Create non-root user
RUN useradd -u 1001 -r -g 0 -s /sbin/nologin \
    -c "Coordination Engine user" coordination-engine

# Set ownership
RUN chown -R 1001:0 /app && \
    chmod -R g=u /app

# Switch to non-root user
USER 1001

# Expose ports
EXPOSE 8080 9090

# Health check (via HTTP endpoint)
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/bin/curl", "-f", "http://localhost:8080/api/v1/health", "||", "exit", "1"]

# Run the binary
ENTRYPOINT ["/app/coordination-engine"]

