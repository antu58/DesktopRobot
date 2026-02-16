FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG APP
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/app ./cmd/${APP}

FROM alpine:3.20
RUN adduser -D -H appuser
USER appuser
WORKDIR /app
COPY --from=builder /out/app /app/app
EXPOSE 9010 9011
ENTRYPOINT ["/app/app"]
