FROM golang:1.21-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY cmd/sync-receiver/main.go ./cmd/sync-receiver/
RUN CGO_ENABLED=0 go build -o /sync-receiver ./cmd/sync-receiver

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /sync-receiver /sync-receiver
EXPOSE 9090
ENTRYPOINT ["/sync-receiver"]
