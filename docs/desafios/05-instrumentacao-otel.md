# Fase 4 — Instrumentação com OpenTelemetry

**Projeto:** ToggleMaster (FIAP — Fase 4)
**Escopo:** `otel.go` (novo), `main.go`, `handlers.go`, `go.mod` e `go.sum`

---

## Visão geral

Primeiro serviço em Go instrumentado da Fase 4. O objetivo do passo não era só emitir telemetria: era **fechar o primeiro trace entre duas linguagens**.

Antes disto, o `flag-service` (Python) já emitia um span de cliente ao chamar `GET /validate`, mas o trace morria ali — o outro lado não falava OTel. Agora uma única requisição a `GET /flags` produz um trace com spans dos dois serviços.

Em Go não existe agente: a linguagem é compilada e não há *monkey patching*. Tudo o que a auto-instrumentação do Python fazia escondido precisou ser escrito. É mais trabalho, e em troca fica visível o que estava acontecendo.

---

## O pré-requisito: propagar `context.Context`

O `traceparent` chega no header, o `otelhttp` o coloca no `context` da requisição, e **só chega ao banco quem levar esse context adiante**.

```diff
-	err := a.DB.QueryRow("SELECT id FROM api_keys WHERE key_hash = $1 ...", keyHash).Scan(&id)
+	err := a.DB.QueryRowContext(r.Context(), "SELECT id FROM api_keys WHERE key_hash = $1 ...", keyHash).Scan(&id)
```

Duas linhas, em `handlers.go:46` e `handlers.go:89`. É pouco porque este serviço é raso — não faz chamadas de saída. No `evaluation-service` a mesma ideia vai alcançar uma dúzia de assinaturas, porque lá há chamadas HTTP, Redis e fila dentro do mesmo fluxo.

Sem isso, os spans de banco nasceriam órfãos: existiriam, mas em traces soltos, sem ligação com a requisição que os originou.

---

## Desafio 1 — a versão mais recente do OTel exige Go 1.26

A primeira tentativa de instalar as dependências parou no build:

```
go: go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@v0.72.0
    requires go >= 1.26.0 (running go 1.25.14; GOTOOLCHAIN=local)
```

A verificação mostrou que não era um pacote isolado — é a linha inteira:

| Módulo | Versão | Go exigido |
|---|---|---|
| `otel`, `sdk`, exporters | v1.47.0 | **1.26.0** |
| `otel`, `sdk`, exporters | v1.46.0 | 1.25.0 |
| `otelhttp` | v0.72.0 | **1.26.0** |
| `otelhttp` | v0.71.0 | 1.25.0 |
| `otelslog` | v0.21.0 | **1.26.0** |
| `otelslog` | v0.20.0 | 1.25.0 |

### Decisão: ficar na 1.46

O projeto está em Go 1.25, e subir o toolchain repetiria o esforço documentado em [`04-cve-x-crypto.md`](04-cve-x-crypto.md) — onde a subida de 1.21 para 1.25 tocou `go.mod`, `Dockerfile` e o workflow de CI.

Lá havia motivo: um CVE **crítico** reprovando a esteira. Aqui não há ganho funcional, apenas uma versão menor mais nova. A subida fica para quando houver razão, e será deliberada.

### Um detalhe de numeração

A API e o SDK de **logs** do OpenTelemetry saltaram de `v0.22.0` direto para `v1.47.0` ao se estabilizarem. Não existe `v1.46.0` dessas bibliotecas, então a linha compatível com Go 1.25 é a `0.22`.

---

## Desafio 2 — o que precisa ser escrito quando não há agente

### `otel.go`

Uma função `setupOTel` que monta os três sinais e devolve um `shutdown`:

| Peça | O que faz |
|---|---|
| `resource` | `resource.WithFromEnv()` lê `OTEL_SERVICE_NAME` e `OTEL_RESOURCE_ATTRIBUTES`. O padrão entra sem código |
| `TracerProvider` | exporter `otlptracegrpc` com `BatchSpanProcessor` |
| `MeterProvider` | exporter `otlpmetricgrpc` com leitor periódico |
| `LoggerProvider` | exporter `otlploggrpc` com `BatchProcessor` |
| **Propagador** | `TraceContext` + `Baggage` |

**O propagador é o item que silenciosamente quebra tudo.** Sem ele o SDK ignora o `traceparent` recebido e inicia um trace novo a cada requisição. Tudo o mais funciona, os spans aparecem, e a correlação entre serviços simplesmente não existe — sem erro nenhum.

### Os pontos de ligação

```diff
-	db, err := sql.Open("pgx", databaseURL)
+	db, err := otelsql.Open("pgx", databaseURL)
```

```go
srv := &http.Server{
    Addr:    ":" + port,
    Handler: instrument(mux),
}
```

O `instrument` embrulha o mux com `otelhttp`, com duas opções que não são detalhe:

- **`WithSpanNameFormatter`** — por padrão todos os spans receberiam o mesmo nome, e `/health`, `/validate` e `/admin/keys` ficariam indistinguíveis no APM. O formatter faz o nome sair como `POST /admin/keys`, equivalente ao que o Flask produz do outro lado.
- **`WithFilter`** — descarta o `/health`, que o healthcheck bate a cada 30s. No Python isso foi uma variável de ambiente (`OTEL_PYTHON_FLASK_EXCLUDED_URLS`); em Go é código.

### Graceful shutdown deixou de ser opcional

O serviço usava `http.ListenAndServe` com `log.Fatal`, o que impede qualquer `defer` de rodar — inclusive o `db.Close()` que já existia em `main.go:44` e nunca executou.

Com `BatchSpanProcessor`, isso significa **perder os últimos spans a cada encerramento**: justamente os do momento em que algo deu errado. O servidor passou a ser um `http.Server` com captura de `SIGTERM`, e o `shutdown` do OTel dá flush nos três providers.

---

## Desafio 3 — logs com contexto exigem sair do `log`

O pacote `log` da stdlib não conhece `context` e não produz JSON. Os treze pontos de log migraram para **`slog`**, com duas consequências.

### Dois destinos, um handler

O `slog` aceita **um** handler. Como o log precisa ir para o stdout (que o `filelog` vai consumir no cluster) e para o Collector, foi preciso um fanout de poucas linhas:

```go
slog.SetDefault(slog.New(multiHandler{
    slog.NewJSONHandler(os.Stdout, nil),
    otelslog.NewHandler(service),
}))
```

### As variantes com contexto

```diff
-	log.Printf("Nova chave criada com sucesso (ID: %d, Name: %s)", newID, req.Name)
+	slog.InfoContext(r.Context(), "Nova chave criada com sucesso", "id", newID, "nome", req.Name)
```

Duas mudanças numa linha só. O `InfoContext` recebe o contexto da requisição, e é ele que prende o trace ao registro. E os valores saem como **campos**, não interpolados na mensagem — o que torna o log consultável por `id` ou por `nome`, em vez de exigir expressão regular.

---

## Resumo das mudanças

| Arquivo | Mudança | Motivo |
|---|---|---|
| `otel.go` | Novo | Resource, três providers, propagador, shutdown e o fanout de log |
| `main.go` | `setupOTel` + `defer shutdown` | Inicialização e flush |
| `main.go` | `otelsql.Open` no lugar de `sql.Open` | Spans de banco |
| `main.go` | `http.Server` com `SIGTERM` | Sem isso o flush nunca acontece |
| `main.go` | `otelhttp` embrulhando o mux | Span de servidor, com nome por rota |
| `handlers.go` | 2× `QueryRow` → `QueryRowContext` | Propagação de contexto |
| `main.go`, `handlers.go` | 13 pontos de `log` → `slog` | JSON no stdout e contexto no registro |
| `go.mod`, `go.sum` | 11 dependências diretas | — |

**O `Dockerfile` não mudou.** Ele copia o `go.mod` e roda `go mod tidy` no build (`Dockerfile:11`), então dependência nova entra sozinha. É o contraste exato com o `flag-service`, onde o `CMD` precisou passar a chamar o wrapper `opentelemetry-instrument`: lá a instrumentação é um processo que embrulha a aplicação, aqui ela é parte do binário.

---

## Validação

Ambiente: `togglemaster-platform-legacy`, com Collector `0.162.0` e exporter `debug`.

### O trace atravessando duas linguagens

Uma requisição `GET /flags`, trace `3c573e7935a6`:

```
flag-service   GET /flags              Server   span=76c0d4fb   (raiz)
├── flag-service   GET                 Client   span=697d991a   parent=76c0d4fb
│   └── auth-service   GET /validate   Server   span=1229203e   parent=697d991a   ← a fronteira
│       ├── auth-service  sql.conn.query          parent=1229203e
│       ├── auth-service  sql.rows                parent=1229203e
│       └── auth-service  sql.conn.reset_session  parent=1229203e
└── flag-service   SELECT              Client   span=250fe0ca   parent=76c0d4fb
```

O span `GET /validate` tem como pai o span de cliente do `flag-service`. O `traceparent` saiu de um processo Python, viajou no header e um processo Go o aceitou e continuou a mesma árvore. É a prova de que o propagador está no lugar.

### O log correlacionado

```
Body: Str(Nova chave criada com sucesso)
SeverityText: INFO
Attributes:
     -> id: Int(4)
     -> nome: Str(demo-logs-go)
Trace ID: 73f40f65bd4db18910cfd2e6432e012b
Span ID: 986ee818b424cb20
```

O `Span ID` do log é o do span `POST /admin/keys` que o originou — correlação no nível do span, não só do trace.

Vale comparar com o Python: lá o `trace_id` chega como **atributo** do registro (`otelTraceID`), enquanto aqui ocupa o **campo nativo** `Trace ID` do registro OTLP. O segundo é o formato canônico; o primeiro é a ponte de logging preenchendo o que consegue.

### Verificações locais

| Verificação | Resultado |
|---|---|
| `go build ./...` | ok |
| `go vet ./...` | ok |
| Serviço em container | sobe, responde e loga em JSON |
| Três sinais no Collector | traces, métricas e logs |

---

## O que não foi feito

**Enxugar os spans do `otelsql`.** Ele emite `sql.conn.query`, `sql.rows` e `sql.conn.reset_session` para uma única consulta — mais fiel ao que o `database/sql` faz, incluindo o ciclo do pool, mas verboso perto do span único que o psycopg2 produz. Dá para reduzir pelas opções do pacote, se a árvore incomodar.

**Métricas de runtime.** Heap, goroutines e GC não vêm sozinhas; exigem um pacote a mais, ligado de propósito.

**Remoção do `demo.go`.** É resíduo de outra demonstração e continua sem efeito sobre o serviço.

**Subida para Go 1.26 e OTel 1.47.** Registrada como decisão adiada, não como esquecimento.
