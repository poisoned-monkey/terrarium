# Multi-stage build для оператора DevEnvironment
FROM golang:1.21-alpine AS builder
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /manager ./cmd/manager

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /manager /manager
USER 65534:65534
ENTRYPOINT ["/manager"]
