FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/go-llm-backend .

FROM alpine:3.20
WORKDIR /app
COPY --from=builder /out/go-llm-backend /app/go-llm-backend

EXPOSE 8090
CMD ["/app/go-llm-backend"]
