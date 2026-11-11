package main

// Convenções do app CDK do categories-service. Os valores que vêm do infra-base
// (prefixo do SSM, nomes de stack e de parâmetro, source dos eventos) são
// cópias de gestor-gastos-infra-base/infra/config.go: se mudarem lá, mudam
// aqui também.

import "fmt"

const (
	// Projeto é o prefixo usado em nomes de stacks e recursos.
	Projeto = "gestor-gastos"

	// Repositorio vai na tag `repositorio` dos recursos.
	Repositorio = "gestor-gastos-categories-service"

	// Regiao é a única região usada pelo sistema.
	Regiao = "us-east-1"

	// NomeServico compõe o nome da stack, da função e da tabela.
	NomeServico = "categories"

	// ChaveContextoAmbiente é a chave de contexto do CDK com o ambiente,
	// a mesma que o go-service.yml passa (`--context env=dev`).
	ChaveContextoAmbiente = "env"

	// AmbientePadrao é usado quando o contexto `env` não é informado.
	AmbientePadrao = "dev"

	// SourceUsers é o `source` do UserDeleted publicado pelo users-service
	// (tarefa 10.1). Segue o padrão do SourceAuth do infra-base
	// ("gestor-gastos.auth"): o publisher do users-service tem de usar este
	// valor, ou a regra não casa.
	SourceUsers = Projeto + ".users"

	// DetailTypeUserDeleted é o detail-type consumido, igual a
	// events.UserDeleted da go-commons.
	DetailTypeUserDeleted = "UserDeleted"

	// TarefaSemearPadrao é o payload {"tarefa": ...} que o custom resource
	// do deploy manda para a função gravar as categorias padrão. Igual a
	// handler.TarefaSemearPadrao.
	TarefaSemearPadrao = "SemearPadrao"

	// AliasLive é o alias que o BFF invoca e que a regra do EventBridge aciona.
	// O CodeDeploy move o tráfego dele de uma versão para a outra.
	AliasLive = "live"
)

// Ambientes implantáveis. Por custo, há um único ambiente na AWS (ADR 0007).
var Ambientes = []string{"dev"}

// Parâmetros lidos do SSM, publicados pela stack events do infra-base.
const (
	ParamBusName = "events/bus-name"
	ParamDlqArn  = "events/dlq-arn"
)

// Parâmetros publicados por esta stack. O BFF lê ParamFunctionArn para
// invocar o serviço; mudar um nome quebra quem consome.
const (
	ParamFunctionArn = "categories/function-arn"
	ParamTableName   = "categories/table-name"
)

// origemParametro diz quem publica cada parâmetro lido, para a mensagem de
// erro apontar o que implantar.
var origemParametro = map[string]string{
	ParamBusName: "stack gestor-gastos-<env>-events do gestor-gastos-infra-base",
	ParamDlqArn:  "stack gestor-gastos-<env>-events do gestor-gastos-infra-base",
}

// ParametrosLidos são os parâmetros do SSM de que a stack depende.
var ParametrosLidos = []string{ParamBusName, ParamDlqArn}

// PrefixoSSM devolve o caminho base dos parâmetros de um ambiente no SSM.
// Ex.: /gestor-gastos/dev.
func PrefixoSSM(env string) string {
	return "/" + Projeto + "/" + env
}

// CaminhoSSM devolve o caminho completo de um parâmetro do ambiente.
// Ex.: CaminhoSSM("dev", ParamFunctionArn) = /gestor-gastos/dev/categories/function-arn.
func CaminhoSSM(env, param string) string {
	return PrefixoSSM(env) + "/" + param
}

// NomeStack devolve o nome de um recurso por ambiente. Ex.: gestor-gastos-dev-categories.
func NomeStack(env, nome string) string {
	return Projeto + "-" + env + "-" + nome
}

// validarAmbiente aceita só os ambientes de Ambientes.
func validarAmbiente(env string) error {
	for _, a := range Ambientes {
		if a == env {
			return nil
		}
	}
	return fmt.Errorf("ambiente %q inválido: use um de %v (contexto %s)", env, Ambientes, ChaveContextoAmbiente)
}
