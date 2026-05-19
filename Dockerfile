# ─── Stage 1: Build ─────────────────────────────────────────────────────────
FROM golang:1.21-alpine AS builder

WORKDIR /app

COPY go.mod .
RUN go mod download

COPY . .

RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o auth-service .

FROM alpine:3.19

RUN apk --no-cache add ca-certificates wget

RUN addgroup -S appgroup && adduser -S -u 10001 appuser -G appgroup

WORKDIR /home/appuser

COPY --from=builder --chown=appuser:appgroup /app/auth-service .

USER appuser

EXPOSE 8001

ENTRYPOINT ["./auth-service"]
