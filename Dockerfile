FROM golang:1.25 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o mcp-launcher .

FROM gcr.io/distroless/static:nonroot
WORKDIR /app
COPY --from=builder /app/mcp-launcher .
COPY --from=builder /app/templates ./templates
USER 65532:65532
ENTRYPOINT ["/app/mcp-launcher"]
