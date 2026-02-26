FROM golang:1.24-alpine AS builder
WORKDIR /src
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG ALL_PROXY
ENV HTTP_PROXY=${HTTP_PROXY} HTTPS_PROXY=${HTTPS_PROXY} ALL_PROXY=${ALL_PROXY}

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/asr-server ./cmd/server

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /out/asr-server /app/asr-server
COPY web /app/web

EXPOSE 8088
ENTRYPOINT ["/app/asr-server", "-addr", ":8088", "-web", "/app/web"]
