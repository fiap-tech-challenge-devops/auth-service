# Stage 1: Build
FROM golang:1.21-alpine AS builder

WORKDIR /app

COPY go.mod .
RUN go mod download

COPY . .

RUN go mod tidy && CGO_ENABLED=0 go build -o auth-service .

# Stage 2: Runtime
FROM alpine:3.19

RUN apk --no-cache add ca-certificates wget

RUN addgroup -S togglemastergroup && adduser -S -u 10001 togglemaster -G togglemastergroup

WORKDIR /home/togglemaster

COPY --from=builder --chown=togglemaster:togglemastergroup /app/auth-service .

USER togglemaster

EXPOSE 8001

ENTRYPOINT ["./auth-service"]
