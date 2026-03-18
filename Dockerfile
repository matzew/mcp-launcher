FROM registry.access.redhat.com/ubi9/go-toolset:1.25.7 AS builder

WORKDIR /opt/app-root/src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -buildvcs=false -o mcp-launcher .

FROM registry.access.redhat.com/ubi9-micro:latest
WORKDIR /app
COPY --from=builder /opt/app-root/src/mcp-launcher .
COPY --from=builder /opt/app-root/src/templates ./templates
USER 65532:65532
ENTRYPOINT ["/app/mcp-launcher"]
