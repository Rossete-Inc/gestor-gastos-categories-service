# gestor-gastos-categories-service

Serviço de categorias do Gestor de Gastos (Go, Lambda, DynamoDB). Guarda as categorias padrão do sistema e as categorias personalizadas de cada usuário, separadas nos grupos `CARTAO` e `FIXO` (spec `categorias`).

## Estrutura

| Pacote | O que faz |
| --- | --- |
| `contract` | Contrato público com o BFF: nomes das operações, grupos, códigos de erro e formatos de entrada e saída. É o único pacote fora de `internal/` |
| `internal/categoria` | Domínio: grupos, as 12 categorias padrão (fonte da verdade), validação, nome único por grupo, imutabilidade das padrão, visibilidade por usuário e ordenação |
| `internal/app` | Casos de uso: listar, obter, criar, excluir, consumir `UserDeleted` e gravar as padrão |
| `internal/store` | Store DynamoDB (chaves abaixo) |
| `internal/handler` | Liga os casos de uso ao `invoke.Router` e ao `events.Router` da go-commons, usados pela Lambda e pelo `cmd/local` |
| `cmd/local` | Servidor HTTP local que chama os mesmos handlers da Lambda (ver "Rodar localmente") |
| `internal/memstore` | Store em memória com a mesma semântica do DynamoDB, para os testes unitários |
| `internal/dynamotest` | Cria tabelas temporárias no DynamoDB Local para os testes de integração |
| `cmd/lambda` | Função na AWS: o mesmo `handler.Lambda` atende o invoke do BFF, o evento `UserDeleted` e a tarefa de seed |
| `infra` | App AWS CDK (Go), módulo separado: stack `gestor-gastos-<env>-categories` (ver "Na AWS") |

## Contrato com o BFF

Toda operação age sobre o `userId` do envelope, que o BFF tira do token. Envelope sem `userId` responde 400 `usuario_ausente`. Erros chegam como problem+json dentro do envelope.

| Operação | Payload | Resposta | Erros |
| --- | --- | --- | --- |
| `ListarCategorias` | — | `{"CARTAO":[Categoria],"FIXO":[Categoria]}`, em ordem alfabética, grupos vazios como `[]` | — |
| `ObterCategoria` | `{"id"}` | `Categoria` | 404 `categoria_nao_encontrada` (inexistente ou de outro usuário) |
| `CriarCategoria` | `{"nome","grupo","cor"}` | `Categoria` | 422 `validacao` com `errors[]` (`nome`, `grupo`, `cor`); 409 `categoria_duplicada` |
| `ExcluirCategoria` | `{"id"}` | sem payload | 403 `categoria_padrao`; 404 `categoria_nao_encontrada` |

`Categoria` é `{"id","nome","grupo","cor","padrao"}`, igual ao schema do `openapi.yaml` do BFF. A checagem de gastos vinculados (409 `categoria_em_uso`) é do BFF, que consulta o expenses-service antes de chamar `ExcluirCategoria` (design.md, "Regras que atravessam serviços ficam no BFF").

Evento consumido: `UserDeleted` (`{"userId"}`), que apaga todas as categorias personalizadas do usuário. É idempotente.

## Regras

- **Grupos:** só `CARTAO` e `FIXO`. Outro valor, ou grupo vazio, é recusado com 422.
- **Categorias padrão:** as 12 da spec, com `PK=SYSTEM`, visíveis a todos e imutáveis. Excluir uma delas responde 403 `categoria_padrao`.
- **Personalizadas:** visíveis só para o dono. Uma categoria de outro usuário responde 404, como se não existisse.
- **Nome único por grupo:** comparado ignorando maiúsculas e minúsculas, espaços nas pontas e espaços repetidos, contra as padrão e as do próprio usuário. Acentos contam: "Agua" e "Água" são nomes diferentes. O mesmo nome em outro grupo é permitido.
- **Normalização:** o nome é gravado sem espaços repetidos e em NFC, com no máximo 40 caracteres. A cor segue `#RRGGBB` e é gravada em maiúsculas.

## DynamoDB

Tabela `gestor-gastos-categories`, com as chaves do contrato com o seed do infra-base e o CDK (`docs/local.md` do infra-base):

| Item | PK | SK |
| --- | --- | --- |
| Categoria padrão | `SYSTEM` | `CAT#<grupo>#<id>` |
| Categoria personalizada | `USER#<id>` | `CAT#<grupo>#<id>` |
| Trava de nome | `USER#<id>` | `NOME#<grupo>#<nome em minúsculas>` |

Os atributos das categorias são os mesmos que o seed grava: `categoriaId`, `dono`, `grupo`, `nome` e `cor`, mais `criadoEm` nas personalizadas. A trava de nome é gravada na mesma transação da categoria e garante o nome único mesmo com duas criações simultâneas. Ela fica na partição do usuário e não aparece nas listagens, que filtram por `CAT#`.

**Fonte da verdade das categorias padrão:** `internal/categoria/padrao.go`. O seed do infra-base (`local/seed/dados.go`) grava uma cópia idêntica no ambiente local, e o `TestPadraoIgualAoSeed` compara as duas quando o infra-base está clonado ao lado deste repositório. O próprio serviço regrava as padrão de forma idempotente (`SemearPadrao`): o `cmd/local` faz isso ao subir, e, na nuvem, um custom resource do CDK invoca a versão nova da função com `{"tarefa":"SemearPadrao"}` a cada deploy que muda o código.

## Rodar localmente

Com o ambiente local do infra-base no ar (`task local:up` e `task local:seed`):

```sh
RUNTIME=local go run ./cmd/local
```

O servidor sobe em http://127.0.0.1:8083, regrava as categorias padrão e chama os mesmos handlers da Lambda:

| Rota | Uso |
| --- | --- |
| `POST /invoke` | Envelope do `invoke`, chamado pelo BFF |
| `POST /events` | Eventos, como `UserDeleted` |
| `GET /healthz` | Responde `ok` |

| Variável | Padrão |
| --- | --- |
| `RUNTIME` | obrigatória; só aceita `local` |
| `PORT` | `8083` |
| `DYNAMODB_ENDPOINT` | `http://localhost:8000`; endpoints fora da máquina são recusados |
| `CATEGORIES_TABLE` | `gestor-gastos-categories` |
| `SEED_PADRAO` | `true`; com `false`, não regrava as categorias padrão ao subir |
| `LOG_LEVEL` | `info` |

**Segurança local:** o `/invoke` confia no `userId` do envelope. Na AWS, só o BFF consegue invocar a Lambda, por IAM. Localmente, o servidor escuta **só em 127.0.0.1**.

Exemplos com `curl`:

```sh
# categorias visíveis (as 12 padrão para um usuário novo)
curl -s -X POST localhost:8083/invoke -H 'Content-Type: application/json' \
  -d '{"operation":"ListarCategorias","userId":"u-demo"}'

# criar uma personalizada; a resposta traz o id gerado
curl -s -X POST localhost:8083/invoke -H 'Content-Type: application/json' \
  -d '{"operation":"CriarCategoria","userId":"u-demo","payload":{"nome":"Pet","grupo":"CARTAO","cor":"#E4572E"}}'

# o mesmo nome com outra caixa: 409 categoria_duplicada
curl -s -X POST localhost:8083/invoke -H 'Content-Type: application/json' \
  -d '{"operation":"CriarCategoria","userId":"u-demo","payload":{"nome":"pet","grupo":"CARTAO","cor":"#E4572E"}}'

# excluir uma padrão: 403 categoria_padrao
curl -s -X POST localhost:8083/invoke -H 'Content-Type: application/json' \
  -d '{"operation":"ExcluirCategoria","userId":"u-demo","payload":{"id":"alimentacao"}}'

# obter e excluir a personalizada (troque <id> pelo id devolvido na criação)
curl -s -X POST localhost:8083/invoke -H 'Content-Type: application/json' \
  -d '{"operation":"ObterCategoria","userId":"u-demo","payload":{"id":"<id>"}}'
curl -s -X POST localhost:8083/invoke -H 'Content-Type: application/json' \
  -d '{"operation":"ExcluirCategoria","userId":"u-demo","payload":{"id":"<id>"}}'

# evento de usuário excluído: apaga as personalizadas dele e responde 204
curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:8083/events -H 'Content-Type: application/json' \
  -d '{"id":"e1","detail-type":"UserDeleted","source":"curl","time":"2026-11-10T18:30:00Z","detail":{"userId":"u-demo"}}'
```

Erros chegam dentro do envelope, por exemplo `{"error":{"status":409,"code":"categoria_duplicada",...}}`. Para inspecionar a tabela, abra o dynamodb-admin em http://localhost:8001.

## Na AWS

A esteira (`.github/workflows/ci.yml`, sobre o `go-service.yml` do infra-base) implanta a stack `gestor-gastos-dev-categories` a cada merge na `main`, no padrão do users-service (tarefa 6.5):

| Recurso | Detalhe |
| --- | --- |
| Tabela `gestor-gastos-dev-categories` | On-demand, `RETAIN`, chaves da seção DynamoDB, sem índices |
| Lambda `gestor-gastos-dev-categories` | arm64, 256 MB, 10 s, alias `live` com canary do CodeDeploy (10% por 5 min) e rollback pelo alarme de erros; logs por 7 dias |
| Regra `gestor-gastos-dev-categories-user-deleted` | `source` `gestor-gastos.users`, `detail-type` `UserDeleted`, com retentativas e a DLQ do barramento. O users-service tem de publicar com esse `source` (tarefa 10.1) |
| Seed das padrão | Custom resource que invoca a versão nova com `{"tarefa":"SemearPadrao"}`; o physical id é o número da versão, então roda sempre que o código muda |
| SSM | `/gestor-gastos/dev/categories/function-arn` (lido pelo BFF) e `/gestor-gastos/dev/categories/table-name` |

Depende do SSM publicado pela stack `events` do infra-base (`events/bus-name` e `events/dlq-arn`). Os testes de assertion rodam com `cd infra && go test ./...`, sem AWS.

## Testes

```sh
go test ./... -cover
```

Os testes de integração (store e `cmd/local` de ponta a ponta) rodam contra o DynamoDB Local do `gestor-gastos-infra-base`. Cada teste cria e apaga uma tabela própria, sem tocar na `gestor-gastos-categories`:

```sh
# no infra-base
task local:up
# aqui
go test -tags integration ./... -v
```

A esteira (`.github/workflows/ci.yml`) roda lint e os testes unitários e de integração em todo PR, com um DynamoDB Local como serviço, além do `cdk diff` contra `dev`.
