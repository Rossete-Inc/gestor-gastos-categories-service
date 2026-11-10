// Package contract é o contrato público do categories-service: os nomes das
// operações chamadas pelo BFF via invoke e os formatos de entrada e saída.
//
// É o único pacote não interno do módulo. O BFF o importa numa versão
// fixada, então mudar um campo aqui é mudança de contrato.
//
// Toda operação age sobre o userId do envelope, que o BFF tira do token.
// Envelope sem userId: 400 "usuario_ausente".
package contract

// Operações atendidas pelo invoke.Router do serviço.
const (
	// OpListarCategorias devolve CategoriasPorGrupo com as categorias
	// visíveis ao usuário: as padrão e as personalizadas dele. Sem payload.
	OpListarCategorias = "ListarCategorias"
	// OpObterCategoria devolve a Categoria de ObterCategoriaIn.ID, se visível
	// ao usuário. Inexistente ou de outro usuário: 404
	// "categoria_nao_encontrada". Usada pelo BFF para validar o categoriaId
	// de um gasto e obter o grupo.
	OpObterCategoria = "ObterCategoria"
	// OpCriarCategoria cria uma categoria personalizada com CriarCategoriaIn e
	// devolve a Categoria criada. Dados inválidos: 422 "validacao" com os
	// campos (nome, grupo, cor). Nome já visível no grupo, ignorando
	// maiúsculas e minúsculas: 409 "categoria_duplicada".
	OpCriarCategoria = "CriarCategoria"
	// OpExcluirCategoria exclui a categoria personalizada de
	// ExcluirCategoriaIn.ID. Sem payload na resposta. Categoria padrão: 403
	// "categoria_padrao". Inexistente ou de outro usuário: 404
	// "categoria_nao_encontrada". A checagem de gastos vinculados
	// ("categoria_em_uso") é do BFF, antes de chamar esta operação.
	OpExcluirCategoria = "ExcluirCategoria"
)

// Grupos de gasto.
const (
	GrupoCartao = "CARTAO"
	GrupoFixo   = "FIXO"
)

// Códigos de erro (campo "code" do problem+json).
const (
	CodigoNaoEncontrada  = "categoria_nao_encontrada"
	CodigoDuplicada      = "categoria_duplicada"
	CodigoPadrao         = "categoria_padrao"
	CodigoUsuarioAusente = "usuario_ausente"
)

// Categoria é a resposta de ObterCategoria e CriarCategoria e o item de
// CategoriasPorGrupo. Tem o mesmo formato do schema Categoria do openapi do
// BFF.
type Categoria struct {
	ID     string `json:"id"`
	Nome   string `json:"nome"`
	Grupo  string `json:"grupo"`
	Cor    string `json:"cor"`
	Padrao bool   `json:"padrao"`
}

// CategoriasPorGrupo é a resposta de ListarCategorias, no formato do schema
// CategoriasPorGrupo do openapi: em ordem alfabética dentro de cada grupo e
// com os dois grupos sempre presentes, mesmo vazios.
type CategoriasPorGrupo struct {
	Cartao []Categoria `json:"CARTAO"`
	Fixo   []Categoria `json:"FIXO"`
}

// ObterCategoriaIn é o payload de ObterCategoria.
type ObterCategoriaIn struct {
	ID string `json:"id"`
}

// CriarCategoriaIn é o payload de CriarCategoria, igual ao CategoriaIn do
// openapi.
type CriarCategoriaIn struct {
	Nome  string `json:"nome"`
	Grupo string `json:"grupo"`
	Cor   string `json:"cor"`
}

// ExcluirCategoriaIn é o payload de ExcluirCategoria.
type ExcluirCategoriaIn struct {
	ID string `json:"id"`
}
