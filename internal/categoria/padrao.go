package categoria

// padrao são as categorias padrão da spec `categorias`.
//
// Fonte da verdade: esta lista. Os IDs são estáveis (os gastos guardam o
// categoriaId) e as cores seguem os tokens category/* do Figma. O seed do
// infra-base (local/seed/dados.go) grava uma cópia idêntica no ambiente
// local, e o próprio serviço reaplica esta lista de forma idempotente
// (app.Service.SemearPadrao): o cmd/local faz isso ao subir, e na nuvem a
// tarefa 7.6 decide o gatilho. O teste TestPadraoIgualAoSeed confere a cópia.
var padrao = []Categoria{
	{ID: "alimentacao", Grupo: Cartao, Nome: "Alimentação", Cor: "#F97316"},
	{ID: "transporte", Grupo: Cartao, Nome: "Transporte", Cor: "#3B82F6"},
	{ID: "lazer", Grupo: Cartao, Nome: "Lazer", Cor: "#A855F7"},
	{ID: "saude", Grupo: Cartao, Nome: "Saúde", Cor: "#EF4444"},
	{ID: "compras", Grupo: Cartao, Nome: "Compras", Cor: "#EC4899"},
	{ID: "outros", Grupo: Cartao, Nome: "Outros", Cor: "#64748B"},
	{ID: "aluguel", Grupo: Fixo, Nome: "Aluguel", Cor: "#0EA5E9"},
	{ID: "luz", Grupo: Fixo, Nome: "Luz", Cor: "#EAB308"},
	{ID: "agua", Grupo: Fixo, Nome: "Água", Cor: "#06B6D4"},
	{ID: "internet", Grupo: Fixo, Nome: "Internet", Cor: "#6366F1"},
	{ID: "telefone", Grupo: Fixo, Nome: "Telefone", Cor: "#14B8A6"},
	{ID: "condominio", Grupo: Fixo, Nome: "Condomínio", Cor: "#84CC16"},
}

// Padrao devolve uma cópia das categorias padrão, já com Dono = DonoSistema.
func Padrao() []Categoria {
	out := make([]Categoria, len(padrao))
	for i, c := range padrao {
		c.Dono = DonoSistema
		out[i] = c
	}
	return out
}
