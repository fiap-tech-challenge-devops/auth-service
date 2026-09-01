# Fase 3 — Retornos de erro descartados (`errcheck`)

**Projeto:** ToggleMaster (FIAP — Fase 3)
**Escopo:** Quatro correções em `handlers.go` e `main.go` para o job `Linter` da esteira de CI

---

## Visão geral

O estágio de lint da esteira de CI reprovava desde a primeira execução com o `reusable-workflows`. Quatro achados, todos do mesmo linter — `errcheck` —, todos apontando para valores de erro descartados.

Este documento registra o diagnóstico caso a caso, porque três dos quatro achados são de baixo impacto e um deles esconde uma falha silenciosa com consequência real. Tratar os quatro como "ruído de lint" teria custado o único que importava.

---

## O que o linter acusou

Execução de referência: [run 33430861653](https://github.com/fiap-tech-challenge-devops/auth-service/actions/runs/33430861653), `golangci-lint 2.13.2`, sem `.golangci.yml` no repositório — portanto com o conjunto padrão de linters.

```
main.go:44       Error return value of `db.Close` is not checked (errcheck)
handlers.go:25   Error return value of `(*encoding/json.Encoder).Encode` is not checked (errcheck)
handlers.go:54   Error return value of `(*encoding/json.Encoder).Encode` is not checked (errcheck)
handlers.go:98   Error return value of `(*encoding/json.Encoder).Encode` is not checked (errcheck)
```

Só o `errcheck` acusou. Os demais linters do conjunto padrão passaram limpos.

### Por que a esteira concluía com sucesso mesmo assim

O job `Linter` do `go-ci` tem `continue-on-error: true`. O GitHub marca o job com **X vermelho** na interface, mas a conclusão do workflow não é afetada — todos os seis últimos runs de CI terminaram `success` com o Linter vermelho.

Isso é deliberado, e está documentado no [`reusable-workflows`](https://github.com/fiap-tech-challenge-devops/reusable-workflows): estilo de código não impede uma imagem de subir, vulnerabilidade impede. A consequência é que o achado da linha 98 ficou visível e ignorado por seis execuções.

---

## O que é o `errcheck`

Em Go, `Encode` e `Close` retornam `error`. Chamá-los como se fossem `void` é sintaticamente válido — o compilador aceita — mas descarta a única informação sobre a falha.

Isso diferencia Go de linguagens com exceções: não há nada que "estoure" sozinho. Um erro ignorado é um erro que nunca aconteceu, do ponto de vista de quem lê o log.

---

## Desafio 1 — `handlers.go:98`, chave criada e não entregue

### O que estava errado

```go
log.Printf("Nova chave criada com sucesso (ID: %d, Name: %s)", newID, req.Name)
w.WriteHeader(http.StatusCreated)
json.NewEncoder(w).Encode(CreateKeyResponse{
    Name:    req.Name,
    Key:     newKey, // Retorna a chave em texto plano pela última vez
    Message: "Guarde esta chave com segurança! Você não poderá vê-la novamente.",
})
```

### Por que é um defeito, e não estilo

O comentário da linha do `Key` diz o essencial: **em texto plano pela última vez**. O banco guarda apenas o hash — `hashAPIKey(newKey)` —, então essa resposta HTTP é a única oportunidade de o cliente conhecer a chave.

A sequência é: grava no banco → loga sucesso → responde `201` → serializa a chave.

Se o `Encode` falhar — cliente desconectado, conexão interrompida, escrita parcial — o estado resultante é:

| | |
|---|---|
| No banco | a chave existe, ativa |
| No cliente | nada; a requisição parece ter falhado |
| No log | *"Nova chave criada com sucesso"* |

O log afirma sucesso **antes** de a resposta sair, então a única evidência do problema se perde. O cliente tenta de novo e sobra uma chave órfã que ninguém possui e ninguém sabe que existe.

### Correção aplicada

```diff
-	log.Printf("Nova chave criada com sucesso (ID: %d, Name: %s)", newID, req.Name)
 	w.WriteHeader(http.StatusCreated)
-	json.NewEncoder(w).Encode(CreateKeyResponse{
+	if err := json.NewEncoder(w).Encode(CreateKeyResponse{
 		Name:    req.Name,
 		Key:     newKey, // Retorna a chave em texto plano pela última vez
 		Message: "Guarde esta chave com segurança! Você não poderá vê-la novamente.",
-	})
+	}); err != nil {
+		log.Printf("ERRO: chave %d criada no banco mas nao entregue ao cliente: %v", newID, err)
+		return
+	}
+
+	log.Printf("Nova chave criada com sucesso (ID: %d, Name: %s)", newID, req.Name)
```

Duas mudanças, e só a primeira é exigida pelo linter:

1. **Checar o retorno do `Encode`.** É o que o `errcheck` pede.
2. **Mover o log de sucesso para depois da resposta sair.** Isso é um passo além do lint. Sem essa mudança, o `errcheck` ficaria satisfeito e o log continuaria mentindo exatamente no caso que interessa: dois `log.Printf` contraditórios na mesma requisição, um dizendo "sucesso" e outro dizendo "não entregue".

A mensagem de erro nomeia o estado real — *criada no banco mas não entregue ao cliente* — em vez de um genérico "erro ao escrever resposta". Quem ler esse log no meio de um incidente precisa saber que há uma linha no banco a reconciliar.

**Nenhuma alteração de comportamento no caminho feliz.** A resposta, o status e o corpo são idênticos.

---

## Desafio 2 — `handlers.go:25` e `:54`, respostas de baixo impacto

### O que estava errado

```go
// healthHandler
json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

// validateKeyHandler
json.NewEncoder(w).Encode(map[string]string{"message": "Chave válida"})
```

### Análise

Mesma classe de problema, consequência muito menor. Os dois devolvem um objeto de um campo, sem estado persistido do outro lado. Se o `Encode` falha, o cliente caiu ou a conexão quebrou — não há ação de recuperação possível, e nada fica inconsistente.

Foram corrigidos por consistência e porque o custo é de três linhas. A alternativa seria silenciar apenas esses dois via configuração, o que exigiria um arquivo de config para poupar seis linhas de código.

### Correção aplicada

```diff
-	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
+	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
+		log.Printf("Erro ao escrever resposta de health: %v", err)
+	}
```

```diff
-	json.NewEncoder(w).Encode(map[string]string{"message": "Chave válida"})
+	if err := json.NewEncoder(w).Encode(map[string]string{"message": "Chave válida"}); err != nil {
+		log.Printf("Erro ao escrever resposta de validacao: %v", err)
+	}
```

Aqui não há `return`, diferente do desafio 1: as duas chamadas já são a última instrução da função, e não há log de sucesso posterior a suprimir.

---

## Desafio 3 — `main.go:44`, `defer db.Close()`

### O que estava errado

```go
defer db.Close()
```

### Por que este não é um defeito

Esta é a forma idiomática em Go, presente em praticamente todo projeto que abre uma conexão. O `errcheck` a marca por padrão, e a maioria das equipes a silencia por configuração.

O motivo é simples: o `defer` roda quando `main` está retornando. Não há para onde propagar o erro, nem ação corretiva possível — o processo está encerrando de qualquer forma.

### Por que foi corrigido mesmo assim

A alternativa era criar um `.golangci.yml` só para excluir esta linha. Um arquivo de configuração que existe para silenciar uma única ocorrência custa mais manutenção do que as cinco linhas da correção, e abre o precedente de resolver achado de lint desligando o linter.

O `log.Printf` também não é inútil: um erro no `Close` costuma indicar transação pendente ou conexão em estado inválido, e ter isso no log é melhor que não ter.

### Correção aplicada

```diff
-	defer db.Close()
+	defer func() {
+		if err := db.Close(); err != nil {
+			log.Printf("Erro ao fechar conexao com o banco: %v", err)
+		}
+	}()
```

O `defer` passa a receber uma função anônima porque `defer` aceita uma chamada, não uma instrução `if`. O `()` final é o que a executa no momento do retorno.

### Evidência: o `defer` nunca executa neste serviço

Teste em container, com `docker stop`: o processo saiu com **exit code 2** e a mensagem do `defer` não apareceu no log — nem a de erro, nem nada.

Não é falha da correção. É que `db.Close()` é inalcançável neste programa, antes e depois da mudança. A razão está no fim de `main`:

```go
log.Printf("Serviço de Autenticação (Go) rodando na porta: %s", port)
if err := http.ListenAndServe(":"+port, mux); err != nil {
    log.Fatal(err)
}
```

Há duas formas de o processo terminar, e nenhuma roda `defer`:

| saída | por que o `defer` não roda |
|---|---|
| `ListenAndServe` retorna erro | `log.Fatal` chama `os.Exit(1)`, que encerra sem executar defers — por definição da linguagem |
| Sinal do sistema (`SIGTERM` do `docker stop`) | não há tratamento de sinal; o runtime encerra o processo direto |

Fora esses dois casos, `ListenAndServe` bloqueia indefinidamente e `main` nunca retorna pelo caminho normal.

Isso **confirma** o que esta seção já argumentava: o achado do `errcheck` na linha 44 nunca foi um defeito. A correção continua valendo pelo motivo original — evitar um `.golangci.yml` criado só para silenciar uma linha —, mas agora com evidência de que o erro que ela trata não pode ocorrer.

Para o `Close` passar a importar seria preciso *graceful shutdown*: capturar `SIGTERM`, chamar `srv.Shutdown(ctx)` e deixar `main` retornar normalmente. Isso altera comportamento e está muito além do que o lint pediu — **não foi feito**, e fica registrado como possível melhoria.

---

## Resumo das mudanças por arquivo

### `handlers.go`

| Linha | O que mudou | Por quê |
|---|---|---|
| 25 | `Encode` com checagem e log | `errcheck`; impacto baixo |
| 56 | `Encode` com checagem e log | `errcheck`; impacto baixo |
| 101 | `Encode` com checagem, `return` e log de erro específico | `errcheck` **e** falha silenciosa: chave persistida sem entrega |
| 110 | log de sucesso movido para depois do `Encode` | o log declarava sucesso antes de a resposta sair |

### `main.go`

| Linha | O que mudou | Por quê |
|---|---|---|
| 44 | `defer db.Close()` vira closure com checagem | `errcheck`; preferido a criar `.golangci.yml` |

Nenhum import novo — `log` já estava presente nos dois arquivos. Nenhuma dependência nova. Nenhuma alteração de comportamento no caminho de sucesso.

---

## O que não foi feito, e por quê

### `.golangci.yml` com exclusões

Excluir `errcheck` para `db.Close` e `(*json.Encoder).Encode` faria o X vermelho sumir sem escrever uma linha de código.

Não foi adotado porque a exclusão de `Encode` teria apagado justamente o achado da linha 98 — o único com consequência real. Uma exceção ampla o bastante para cobrir os três casos de baixo impacto cobre também o caso que importa, e a diferença entre eles não é expressável por regra.

### Fazer o `Linter` bloquear a esteira

Está fora do escopo deste repositório: o `continue-on-error` vive no `go-ci` do `reusable-workflows` e vale para os dois serviços em Go. A decisão de não bloquear está registrada lá.

### Verificar os outros microsserviços

O `evaluation-service` também é Go e pode ter o mesmo padrão. Não foi analisado.

---

## Como validar

O `errcheck` não roda localmente sem o toolchain Go instalado. A verificação acontece na esteira:

1. Abrir PR a partir de `fix/fix-lint`
2. Conferir o job `ci / Linter` — deve concluir sem os quatro achados
3. Conferir o job `ci / Build and Unit Test` — confirma que o código compila

Localmente, com Go disponível:

```bash
go build ./...
go vet ./...
golangci-lint run
```

### Verificação em container

As correções foram escritas sem compilador disponível na máquina. Com o Docker no ar depois, a validação foi feita de ponta a ponta:

```bash
docker build -t auth-service:lint-test .
```

O build executa `go build` dentro do `golang:1.24.13-alpine` — **é a compilação**. Passou; imagem final de 36.6 MB.

Em seguida, o serviço rodando contra um PostgreSQL com o `db/init.sql` aplicado, exercitando os três handlers alterados:

| requisição | resposta |
|---|---|
| `GET /health` | `200` `{"status":"ok"}` |
| `POST /admin/keys` com a `MASTER_KEY` | `201`, chave no corpo |
| `GET /validate` com a chave criada | `200` `{"message":"Chave válida"}` |
| `GET /validate` com chave inexistente | `401` |
| `POST /admin/keys` com `MASTER_KEY` errada | `403` |

O ciclo criar → validar fechou: a chave devolvida pelo `Encode` da linha 101 autenticou na chamada seguinte. É a prova de que a mudança preserva o corpo da resposta.

No banco, a chave persistiu com `key_hash` de 64 caracteres e `is_active = true`. No log, uma única linha de sucesso por criação, sem duplicação.

### Antes de haver container: o que foi verificado estaticamente

- balanceamento de chaves nos dois arquivos
- `log` importado em ambos
- CRLF e UTF-8 preservados; acentos nas strings intactos
- o padrão `if err := ...; err != nil` **já existia** no mesmo `createKeyHandler`, na linha 69 (`json.NewDecoder`) — o arquivo compila hoje com esse sombreamento de `err`, o que confirma a validade da construção usada nas linhas 25, 56 e 101

Isso cobria sintaxe, não compilação. O build em container substituiu essa verificação.
