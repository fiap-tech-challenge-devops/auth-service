# Documentação — auth-service

Registro dos desafios técnicos e correções aplicadas durante o desenvolvimento do ToggleMaster (FIAP). A numeração acompanha a fase do Tech Challenge.

Cada documento descreve: o problema, a causa, o erro observado, como foi descoberto, a correção e como validar.

## Desafios

| # | Documento | Escopo |
|---|-----------|--------|
| 01 | [Containerização](desafios/01-containerizacao.md) | Dockerfile, build Docker, correções de compilação e `go.mod` |
| 03 | [Retornos de erro descartados](desafios/03-lint-errcheck.md) | `errcheck` no job Linter da esteira de CI |
| 04 | [CVE em dependência indireta](desafios/04-cve-x-crypto.md) | `x/crypto` crítico e o salto para Go 1.25 |
