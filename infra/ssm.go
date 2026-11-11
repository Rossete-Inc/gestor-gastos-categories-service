package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-cdk-go/awscdk/v2/awsssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// Parametros guarda o valor de cada parâmetro lido (ParamBusName etc.).
type Parametros map[string]string

// Como os parâmetros do infra-base chegam à stack:
//
//   - Na esteira (variável AMBIENTE definida pelo go-service.yml) ou com
//     `-c lerSsm=true`, o app lê o SSM no synth, com as credenciais do job, e
//     grava os valores no template. Se faltar algum, o synth para com uma
//     mensagem dizendo o que implantar antes. A leitura no synth é obrigatória
//     na esteira: a role de deploy do bootstrap do CDK só pode ler o
//     parâmetro de versão do bootstrap, então um parâmetro do CloudFormation
//     do tipo AWS::SSM::Parameter::Value falharia no CreateChangeSet com
//     AccessDenied em ssm:GetParameters.
//   - Fora da esteira (synth e testes offline, sem credenciais), cada valor
//     vira um parâmetro do CloudFormation resolvido no deploy
//     (valueForStringParameter), como o lerParametro do infra-base.
const (
	// ChaveContextoLerSsm força (true) ou desliga (false) a leitura no synth.
	ChaveContextoLerSsm = "lerSsm"
	// VariavelAmbienteEsteira é definida pelo go-service.yml no diff e no deploy.
	VariavelAmbienteEsteira = "AMBIENTE"
)

// SSMAPI é o subconjunto do client SSM usado aqui.
type SSMAPI interface {
	GetParameters(ctx context.Context, in *ssm.GetParametersInput, opt ...func(*ssm.Options)) (*ssm.GetParametersOutput, error)
}

// LerParametrosSSM lê de uma vez os ParametrosLidos do ambiente. Se algum
// faltar, devolve um erro que lista todos os ausentes e quem os publica.
func LerParametrosSSM(ctx context.Context, api SSMAPI, env string) (Parametros, error) {
	caminhos := make([]string, len(ParametrosLidos))
	porCaminho := map[string]string{}
	for i, p := range ParametrosLidos {
		caminhos[i] = CaminhoSSM(env, p)
		porCaminho[caminhos[i]] = p
	}

	out, err := api.GetParameters(ctx, &ssm.GetParametersInput{Names: caminhos})
	if err != nil {
		return nil, fmt.Errorf("não foi possível ler os parâmetros do SSM em %s (%s): %w. "+
			"Confira as credenciais da AWS (na esteira, a role gh-deploy-<repo>-%s ou gh-diff-<repo>) "+
			"ou, para o synth offline, rode sem a variável %s ou com -c %s=false",
			PrefixoSSM(env), Regiao, err, env, VariavelAmbienteEsteira, ChaveContextoLerSsm)
	}

	valores := Parametros{}
	for _, p := range out.Parameters {
		if p.Name != nil && p.Value != nil {
			valores[porCaminho[*p.Name]] = *p.Value
		}
	}

	var faltando []string
	for _, c := range caminhos {
		if v := valores[porCaminho[c]]; v == "" {
			faltando = append(faltando, c+" (publicado pela "+strings.ReplaceAll(origemParametro[porCaminho[c]], "<env>", env)+")")
		}
	}
	if len(faltando) > 0 {
		sort.Strings(faltando)
		return nil, fmt.Errorf("faltam parâmetros no SSM (%s), necessários para a stack %s:\n  - %s\n"+
			"Implante o gestor-gastos-infra-base antes do categories-service (infra/README.md do infra-base) "+
			"e confira com: aws ssm get-parameters-by-path --path %s --recursive --region %s",
			Regiao, NomeStack(env, NomeServico), strings.Join(faltando, "\n  - "), PrefixoSSM(env), Regiao)
	}
	return valores, nil
}

// ParametrosCloudFormation devolve os ParametrosLidos como parâmetros do
// CloudFormation, resolvidos só no deploy. Serve para o synth offline.
func ParametrosCloudFormation(scope constructs.Construct, env string) Parametros {
	valores := Parametros{}
	for _, p := range ParametrosLidos {
		valores[p] = *awsssm.StringParameter_ValueForStringParameter(scope, jsii.String(CaminhoSSM(env, p)), nil)
	}
	return valores
}

// publicarParametro grava um valor no SSM em /gestor-gastos/<env>/<param>, como
// o infra-base. Parâmetros Standard não têm custo.
func publicarParametro(scope constructs.Construct, env, param, valor, descricao string) awsssm.StringParameter {
	return awsssm.NewStringParameter(scope, jsii.String("Param-"+param), &awsssm.StringParameterProps{
		ParameterName: jsii.String(CaminhoSSM(env, param)),
		StringValue:   jsii.String(valor),
		Description:   jsii.String(descricao),
		Tier:          awsssm.ParameterTier_STANDARD,
	})
}
