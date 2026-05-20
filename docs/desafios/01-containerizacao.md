# Documentação de Containerização — auth-service

**Projeto:** ToggleMaster (FIAP — Fase 2)  
**Data:** Maio de 2026  
**Escopo:** Containerização do microserviço de autenticação em Go

---

## Visão Geral

Este documento registra todos os desafios encontrados durante o processo de containerização do `auth-service` e as correções aplicadas. As mudanças foram **estritamente necessárias para compilação e execução em container** — nenhuma lógica de negócio foi alterada.

---

## Artefatos criados

| Arquivo | Local | Descrição |
|---------|-------|-----------|
| `Dockerfile` | `auth-service/` | Imagem Docker multi-stage para o serviço Go |
| `.dockerignore` | `auth-service/` | Exclusão de arquivos desnecessários do contexto de build |
| `docker-compose.yml` | `togglemaster-platform/` | Orquestração de Postgres + auth-service |
| `.env.example` | `togglemaster-platform/` | Documentação das variáveis de ambiente |
| `README.md` | `togglemaster-platform/` | Instruções de uso da plataforma |

---

## Desafio 1 — Entrada inválida no `go.mod`

### O que estava errado

O arquivo `go.mod` continha a seguinte linha no bloco de dependências indiretas:

```
// go.mod (trecho problemático)
require (
    ...
    github.com/jackc/pgx/v4/stdlib v4.18.3 // indirect  ← INVÁLIDO
    ...
)
```

### Por que é inválido

Em Go, o sistema de módulos (`go mod`) trata cada entrada no `require` como um **módulo independente**. A versão de um módulo segue a convenção de versionamento semântico com um detalhe importante: quando a versão principal é 2 ou maior, o caminho do módulo **deve** incluir o sufixo `/vN` para ser válido.

O pacote `github.com/jackc/pgx/v4/stdlib` **não é um módulo separado** — ele é apenas um **pacote** (subdiretório) dentro do módulo `github.com/jackc/pgx/v4`. Portanto:

- O módulo correto é: `github.com/jackc/pgx/v4` (já declarado na linha anterior)
- `stdlib` é apenas um sub-pacote acessado via `import "github.com/jackc/pgx/v4/stdlib"`
- Declarar `github.com/jackc/pgx/v4/stdlib v4.18.3` como módulo separado é semanticamente incorreto: o Go interpretaria isso como um módulo chamado `github.com/jackc/pgx/v4/stdlib` na versão `v4.18.3`, mas como o caminho termina em `/stdlib` (e não em `/v4`), a versão `v4.x.x` é incompatível com o caminho declarado

### Erro gerado no Docker

```
go: errors parsing go.mod:
go.mod:18:2: require github.com/jackc/pgx/v4/stdlib:
  version "v4.18.3" invalid: should be v0 or v1, not v4
```

### Como foi descoberto

O erro apareceu na etapa `RUN go mod download` do Dockerfile durante o primeiro `docker compose up --build`. Localmente, este problema passava despercebido porque o código nunca havia sido compilado com `go build` direto — o fluxo do README usa `go run .`, que pode ter comportamento ligeiramente diferente na resolução de módulos em certas versões do toolchain Go.

### Correção aplicada

Removida a linha inválida do `go.mod`:

```diff
 require (
     github.com/jackc/chunkreader/v2 v2.0.1 // indirect
     github.com/jackc/pgconn v1.14.3 // indirect
     github.com/jackc/pgio v1.0.0 // indirect
     github.com/jackc/pgpassfile v1.0.0 // indirect
     github.com/jackc/pgproto3/v2 v2.3.3 // indirect
     github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
     github.com/jackc/pgtype v1.14.0 // indirect
-    github.com/jackc/pgx/v4/stdlib v4.18.3 // indirect
     github.com/pkg/errors v0.9.1 // indirect
     golang.org/x/crypto v0.20.0 // indirect
     golang.org/x/text v0.14.0 // indirect
 )
```

O acesso ao pacote `stdlib` continua funcionando normalmente via `import _ "github.com/jackc/pgx/v4/stdlib"` no código-fonte, pois esse pacote faz parte do módulo `github.com/jackc/pgx/v4` que permanece declarado.

---

## Desafio 2 — Imports não utilizados no código Go

### O que estava errado

Go é uma linguagem que **não permite imports não utilizados** — isso é um erro de compilação, não um aviso. O compilador rejeita qualquer arquivo `.go` que importe um pacote sem referenciá-lo. Foram encontrados cinco casos:

| Arquivo | Import problemático | Motivo |
|---------|--------------------|----|
| `handlers.go` | `"crypto/sha256"` | Pacote importado mas não usado diretamente neste arquivo |
| `handlers.go` | `"encoding/hex"` | Pacote importado mas não usado diretamente neste arquivo |
| `key.go` | `"fmt"` | Pacote importado mas não usado em nenhuma função |
| `main.go` | `"fmt"` | Pacote importado mas não usado em nenhuma função |
| `main.go` | `"github.com/jackc/pgx/v4/stdlib"` | Import de efeito colateral declarado incorretamente |

### Análise caso a caso

#### `handlers.go` — `crypto/sha256` e `encoding/hex`

O arquivo `handlers.go` usa a função `hashAPIKey()` em dois lugares:

```go
// handlers.go
keyHash := hashAPIKey(keyString)   // linha 42
newKeyHash := hashAPIKey(newKey)   // linha 83
```

A função `hashAPIKey` utiliza `crypto/sha256` e `encoding/hex` internamente — mas ela está **definida em `key.go`**, não em `handlers.go`. Portanto, `handlers.go` não precisa importar esses pacotes; basta chamar a função que já está no mesmo pacote (`package main`).

Em Go, todos os arquivos `.go` de um mesmo pacote compartilham o mesmo escopo — funções definidas em `key.go` são acessíveis diretamente em `handlers.go` sem nenhum import adicional. Os imports devem existir apenas no arquivo onde o pacote é **efetivamente utilizado**.

#### `key.go` — `fmt`

O pacote `fmt` foi importado mas nenhuma função dele (`fmt.Println`, `fmt.Sprintf`, `fmt.Errorf`, etc.) é chamada em `key.go`. Provavelmente foi adicionado durante o desenvolvimento e não removido após uma refatoração.

#### `main.go` — `fmt`

O mesmo caso: `fmt` importado sem uso em `main.go`. Mensagens de log são feitas via `log.Printf` e `log.Fatal` (pacote `log`), que já está importado corretamente.

#### `main.go` — `"github.com/jackc/pgx/v4/stdlib"` (import de efeito colateral)

Este é o caso mais sutil. O código usa `database/sql` com o driver `pgx`:

```go
// main.go — connectDB
db, err := sql.Open("pgx", databaseURL)
```

Para que `sql.Open("pgx", ...)` funcione, o driver `pgx` precisa ser **registrado** no subsistema `database/sql`. Esse registro acontece automaticamente via a função `init()` do pacote `github.com/jackc/pgx/v4/stdlib` — mas apenas se o pacote for importado.

Quando um pacote é importado **exclusivamente pelo efeito colateral** do seu `init()`, Go exige a sintaxe de **blank import**:

```go
import _ "github.com/jackc/pgx/v4/stdlib"
```

O underscore `_` diz ao compilador: *"Importo este pacote intencionalmente, apenas para seus efeitos colaterais (init), e não vou usar nenhum símbolo exportado dele."* Sem o `_`, o compilador vê o import como não utilizado e falha.

O código original tinha:

```go
import "github.com/jackc/pgx/v4/stdlib"  // ← compilador rejeita: nenhum símbolo usado
```

### Erro gerado no Docker

```
# auth-service
./handlers.go:4:2: "crypto/sha256" imported and not used
./handlers.go:5:2: "encoding/hex" imported and not used
./key.go:7:2: "fmt" imported and not used
./main.go:5:2: "fmt" imported and not used
./main.go:10:2: "github.com/jackc/pgx/v4/stdlib" imported and not used
```

### Como foi descoberto

Esses erros apareceram na etapa `RUN go mod tidy && go build` do Dockerfile, após a correção do Desafio 1. Novamente, o fluxo `go run .` descrito no README pode ter mascarado esses erros em algumas versões antigas do toolchain ou em ambientes onde o módulo cache já estava populado de uma forma que relaxava certas verificações.

### Correções aplicadas

**`handlers.go`** — removidos os imports desnecessários:

```diff
 import (
-    "crypto/sha256"
-    "encoding/hex"
     "encoding/json"
     "log"
     "net/http"
     "strings"
 )
```

**`key.go`** — removido `fmt`:

```diff
 import (
     "crypto/rand"
     "crypto/sha256"
     "encoding/hex"
-    "fmt"
 )
```

**`main.go`** — removido `fmt`, corrigido import de efeito colateral:

```diff
 import (
     "database/sql"
-    "fmt"
     "log"
     "net/http"
     "os"
 
-    "github.com/jackc/pgx/v4/stdlib"
+    _ "github.com/jackc/pgx/v4/stdlib"
     "github.com/joho/godotenv"
 )
```

---

## Desafio 3 — Ausência do arquivo `go.sum` no build Docker

### O que estava errado

O repositório não possuía o arquivo `go.sum`. Este arquivo é gerado automaticamente pelo toolchain Go e contém os **hashes criptográficos** de cada módulo baixado, garantindo integridade e reprodutibilidade das dependências.

### Por que `go.sum` não existia

O fluxo de desenvolvimento local usava `go run .` sem nunca executar `go mod tidy` ou `go build` de forma limpa. Em alguns cenários, o `go.sum` pode não ser gerado se o módulo cache do sistema já contiver as dependências de uma instalação anterior, evitando o download e, consequentemente, a geração do arquivo de checksums.

### Impacto no build Docker

O build Docker parte de um ambiente completamente limpo (imagem `golang:1.21-alpine` sem cache de módulos). A estratégia inicial no Dockerfile era:

```dockerfile
COPY go.mod .
RUN go mod download   # baixa deps e deveria gerar go.sum
COPY . .
RUN go build ...      # falhou: go.sum incompleto
```

O problema: `go mod download` com apenas o `go.mod` presente baixa os módulos para o cache interno do container, mas o `go.sum` gerado fica **incompleto** — ele não consegue resolver todos os pacotes que serão efetivamente importados no código-fonte, pois os arquivos `.go` ainda não foram copiados.

O Go 1.21 usa `-mod=readonly` por padrão, que exige um `go.sum` completo e válido antes de compilar. Com um `go.sum` incompleto, o build falha:

```
missing go.sum entry for module providing package github.com/jackc/pgx/v4/stdlib
missing go.sum entry for module providing package github.com/joho/godotenv
```

### Solução adotada

A correção foi adicionar `go mod tidy` **após** o `COPY . .` (quando os arquivos `.go` já estão disponíveis), encadeado com o `go build`:

```dockerfile
COPY go.mod .
RUN go mod download        # pré-aquece o cache de módulos (otimização de layer)

COPY . .
RUN go mod tidy && \       # gera go.sum completo lendo os imports reais do código
    CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o auth-service .
```

O `go mod tidy`:
1. Lê todos os arquivos `.go` do projeto
2. Determina exatamente quais pacotes são importados
3. Garante que `go.mod` e `go.sum` estejam completos e consistentes com o código

O `go mod download` antes do `COPY . .` é mantido como **otimização de cache de layers**: se o `go.mod` não mudar entre builds, essa camada fica em cache e o `go mod tidy` posterior é muito mais rápido (os módulos já estão no cache do container).

### Por que não simplesmente commitar o `go.sum`?

O `go.sum` deveria ser versionado no Git — esta é uma boa prática recomendada pela própria documentação do Go. Com `go.sum` no repositório, a linha do Dockerfile ficaria simplesmente:

```dockerfile
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build ...
```

A estratégia com `go mod tidy` é uma solução robusta para o contexto atual, onde o `go.sum` não existe no repositório. **Recomenda-se gerar e commitar o `go.sum` executando `go mod tidy` localmente após as correções.**

---

## Desafio 4 — Estratégia de build Docker sem cache de dependências

### Contexto

Como não havia `go.sum` no repositório e o `go mod download` sozinho não era suficiente, foi necessário calibrar a ordem das instruções no Dockerfile para equilibrar:

1. **Corretude**: o build precisa funcionar de forma reprodutível
2. **Eficiência**: rebuilds frequentes não devem baixar todas as dependências do zero
3. **Segurança**: sem segredos na imagem, processo não-root, binário estático

### Dockerfile final explicado

```dockerfile
# ─── Stage 1: Build ──────────────────────────────────────────────────────────
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copia apenas go.mod primeiro — se ele não mudar, a próxima linha fica em cache
COPY go.mod .
RUN go mod download   # pré-aquece o cache; layer é reutilizada em rebuilds

# Agora copia o código-fonte (invalida cache somente quando o código muda)
COPY . .

# go mod tidy garante go.sum completo; go build produz binário estático
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o auth-service .

# ─── Stage 2: Runtime enxuto ─────────────────────────────────────────────────
FROM alpine:3.19

# ca-certificates: necessário para conexões TLS (ex: PostgreSQL com SSL)
# wget: necessário para o healthcheck do Docker Compose
RUN apk --no-cache add ca-certificates wget

# Usuário não-root (UID 10001) — boa prática de segurança em containers
RUN addgroup -S appgroup && adduser -S -u 10001 appuser -G appgroup

WORKDIR /home/appuser

# Copia apenas o binário — o stage de build (com Go toolchain) é descartado
COPY --from=builder --chown=appuser:appgroup /app/auth-service .

USER appuser   # processo roda sem privilégios de root

EXPOSE 8001

ENTRYPOINT ["./auth-service"]   # forma exec (sem shell) — sem overhead de /bin/sh
```

**Vantagens do multi-stage:**
- A imagem final contém apenas o binário (~10-15 MB) em vez de toda a toolchain Go (~300 MB)
- O binário é estático (`CGO_ENABLED=0`): não depende de bibliotecas C do sistema operacional
- Compatível com Kubernetes (sem dependências de runtime além do próprio binário)

---

## Resumo das mudanças por arquivo

### `auth-service/go.mod`
- **O que mudou:** Removida a linha `github.com/jackc/pgx/v4/stdlib v4.18.3 // indirect`
- **Por quê:** Entrada inválida no sistema de módulos Go — `stdlib` é um pacote dentro do módulo `pgx/v4`, não um módulo independente. O `go mod download` rejeitava o `go.mod` com esse erro

### `auth-service/handlers.go`
- **O que mudou:** Removidos os imports `"crypto/sha256"` e `"encoding/hex"`
- **Por quê:** Esses pacotes são usados pela função `hashAPIKey()`, que está definida em `key.go`. Em Go, todos os arquivos de um mesmo pacote compartilham o escopo — importar no arquivo errado causa erro de compilação

### `auth-service/key.go`
- **O que mudou:** Removido o import `"fmt"`
- **Por quê:** O pacote `fmt` não é utilizado em nenhuma linha de `key.go`. Go não permite imports sem uso

### `auth-service/main.go`
- **O que mudou:** Removido `"fmt"`; `"github.com/jackc/pgx/v4/stdlib"` alterado para `_ "github.com/jackc/pgx/v4/stdlib"`
- **Por quê (fmt):** Não utilizado no arquivo
- **Por quê (stdlib):** O pacote é importado apenas para registrar o driver `pgx` no `database/sql` via `init()`. Imports de efeito colateral exigem a sintaxe de blank import com `_`, caso contrário o compilador rejeita por "não utilizado"

### `auth-service/Dockerfile` *(novo)*
- Adicionado `go mod tidy` antes do `go build` para gerar `go.sum` completo a partir dos imports reais do código-fonte

---

## Lições aprendidas

### Por que esses erros não apareciam localmente?

O fluxo de desenvolvimento documentado no README usa `go run .`. O comportamento do `go run` difere do `go build` em alguns aspectos de validação de módulos e, dependendo da versão do toolchain e do estado do cache de módulos local (`$GOPATH/pkg/mod`), erros de `go.sum` incompleto e até imports mal declarados podem ser silenciados ou contornados automaticamente.

Em um container Docker, o ambiente é **completamente limpo e determinístico**: sem cache de módulos pré-existente, sem `go.sum` gerado de sessões anteriores, sem estado local. Isso expõe problemas que existiam no código desde o início mas que o ambiente de desenvolvimento local encobria.

### Recomendações para o repositório

1. **Commitar o `go.sum`:** Executar `go mod tidy` localmente e versionar o `go.sum` gerado. Isso torna o build mais rápido e remove a necessidade do `go mod tidy` no Dockerfile
2. **Rodar `go build ./...` localmente antes de commitar:** Diferente de `go run .`, o `go build` aplica todas as validações estritas do compilador, incluindo imports não utilizados
3. **Adicionar CI básico:** Uma pipeline que execute `go build ./...` e `go vet ./...` em cada push detectaria esses problemas automaticamente antes que chegassem ao processo de containerização

---

## Decisões de containerização (sem alteração de código)

Registro de escolhas conscientes durante o MVP, para o relatório e para alinhar com deploy manual (sem CI por enquanto).

### Decisão 1 — Não versionar `go.sum`; manter `go mod tidy` no Dockerfile

**Contexto:** Em ambiente produtivo maduro, `go.mod` e `go.sum` costumam ir juntos no Git e o Dockerfile faz só `go mod download` + `go build`. Neste MVP, o repositório não commita `go.sum`.

**Decisão:** Manter `go mod tidy` na etapa de build do Docker, após `COPY . .`, para gerar/atualizar o sum dentro do container limpo e permitir compilação a partir do código versionado, sem exigir `go mod tidy` na máquina do desenvolvedor antes de cada deploy.

**Trade-off aceito:**

- Build Docker um pouco mais lento quando a layer de código invalida (tidy em todo rebuild).
- Menos alinhado ao fluxo “lockfile no Git” de produção plena.
- Adequado para FIAP/MVP com deploy manual e dependências estáveis.

**Alternativa não adotada:** Rodar `go mod tidy` localmente, commitar `go.sum` e remover o `tidy` do Dockerfile.

---

### Decisão 2 — Simplificação da linha de build no Dockerfile

**Contexto:** A linha original usava flags extras no `go build`:

```dockerfile
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o auth-service .
```

**Decisão:** Simplificar para:

```dockerfile
RUN go mod tidy && CGO_ENABLED=0 go build -o auth-service .
```

**O que foi removido e por quê:**

| Flag removida | Motivo |
|---------------|--------|
| `GOOS=linux` | Redundante: o build já roda dentro de `golang:1.21-alpine` (Linux). |
| `-a` | Rebuild forçado de todos os pacotes; desnecessário neste fluxo Docker. |
| `-installsuffix cgo` | Legado com `CGO_ENABLED=0`; Go 1.21 não exige no nosso caso. |

**O que foi mantido:**

| Item | Motivo |
|------|--------|
| `go mod tidy` | Alinhado à Decisão 1 (sem `go.sum` no Git). |
| `CGO_ENABLED=0` | Binário estático compatível com Alpine no stage final. |

**Validação:** `docker compose build auth-service` e `GET /health` em `http://localhost:8001/health`.

**Impacto no código da aplicação:** Nenhum. Alteração apenas no `Dockerfile`.
