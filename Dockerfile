# ─── Stage 1: Build ─────────────────────────────────────────────────────────
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Baixa dependências antes de copiar o código para aproveitar o cache de layers
COPY go.mod .
RUN go mod download

# Copia o restante do código-fonte
COPY . .

# Completa go.sum com todos os pacotes importados e compila binário estático
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o auth-service .

# ─── Stage 2: Runtime enxuto ─────────────────────────────────────────────────
FROM alpine:3.19

# Certificados TLS necessários para conexões externas e wget para healthcheck
RUN apk --no-cache add ca-certificates wget

# Usuário não-root dedicado (UID 10001)
RUN addgroup -S appgroup && adduser -S -u 10001 appuser -G appgroup

WORKDIR /home/appuser

# Copia apenas o binário compilado do stage de build
COPY --from=builder --chown=appuser:appgroup /app/auth-service .

USER appuser

EXPOSE 8001

ENTRYPOINT ["./auth-service"]
