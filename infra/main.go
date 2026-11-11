// Command infra é o app AWS CDK (Go) do categories-service.
//
// Stack do ambiente (contexto `env`, padrão dev):
//   - gestor-gastos-<env>-categories: tabela, Lambda com alias live e canary
//     do CodeDeploy, regra do EventBridge para UserDeleted, seed das
//     categorias padrão e parâmetros no SSM.
//
// Lê do SSM o que o infra-base publicou (barramento e DLQ) e publica no SSM o
// que o BFF usa (ARN do alias). Nunca usa CloudFormation exports.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// NovoApp monta o app. Separado de main para os testes.
func NovoApp(app awscdk.App, env string, codigo awslambda.Code, params Parametros) awscdk.Stack {
	awscdk.Tags_Of(app).Add(jsii.String("projeto"), jsii.String(Projeto), nil)
	awscdk.Tags_Of(app).Add(jsii.String("repositorio"), jsii.String(Repositorio), nil)
	awscdk.Tags_Of(app).Add(jsii.String("ambiente"), jsii.String(env), nil)

	return NovaStackCategories(app, NomeStack(env, NomeServico), &CategoriesProps{
		StackProps: awscdk.StackProps{Env: ambienteAws()},
		Ambiente:   env,
		Codigo:     codigo,
		Parametros: params,
	})
}

// Ambiente lê o contexto `env` (padrão dev) e aceita só os de Ambientes.
func Ambiente(scope constructs.Construct) (string, error) {
	env := AmbientePadrao
	if v, ok := scope.Node().TryGetContext(jsii.String(ChaveContextoAmbiente)).(string); ok && v != "" {
		env = v
	}
	return env, validarAmbiente(env)
}

// deveLerSsm decide se os parâmetros do infra-base são lidos no synth (ver
// ssm.go). O contexto lerSsm manda; sem ele, lê quando a variável AMBIENTE
// existe, isto é, na esteira.
func deveLerSsm(scope constructs.Construct, lookup func(string) (string, bool)) bool {
	switch v := scope.Node().TryGetContext(jsii.String(ChaveContextoLerSsm)).(type) {
	case bool:
		return v
	case string:
		if v != "" {
			return strings.EqualFold(v, "true")
		}
	}
	amb, _ := lookup(VariavelAmbienteEsteira)
	return amb != ""
}

// parametrosDoSynth devolve os parâmetros lidos do SSM ou nil (parâmetros do
// CloudFormation), conforme deveLerSsm.
func parametrosDoSynth(ctx context.Context, scope constructs.Construct, env string, lookup func(string) (string, bool), novoSSM func(context.Context) (SSMAPI, error)) (Parametros, error) {
	if !deveLerSsm(scope, lookup) {
		return nil, nil
	}
	api, err := novoSSM(ctx)
	if err != nil {
		return nil, err
	}
	return LerParametrosSSM(ctx, api, env)
}

func clienteSSM(ctx context.Context) (SSMAPI, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(Regiao))
	if err != nil {
		return nil, fmt.Errorf("carregar configuração da AWS: %w", err)
	}
	return ssm.NewFromConfig(cfg), nil
}

func run() error {
	defer jsii.Close()

	app := awscdk.NewApp(nil)
	env, err := Ambiente(app)
	if err != nil {
		return err
	}
	params, err := parametrosDoSynth(context.Background(), app, env, os.LookupEnv, clienteSSM)
	if err != nil {
		return err
	}
	NovoApp(app, env, CodigoLambda(DirLambda), params)
	app.Synth(nil)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

// ambienteAws fixa a região em us-east-1. A conta vem das credenciais do CLI
// (CDK_DEFAULT_ACCOUNT) quando houver; sem credenciais a stack fica agnóstica
// de conta, o que permite synth e testes offline.
func ambienteAws() *awscdk.Environment {
	env := &awscdk.Environment{Region: jsii.String(Regiao)}
	if conta := os.Getenv("CDK_DEFAULT_ACCOUNT"); conta != "" {
		env.Account = jsii.String(conta)
	}
	return env
}
