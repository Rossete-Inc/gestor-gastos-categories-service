package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// ssmFalso devolve os parâmetros do mapa e lista os demais como inválidos,
// como o GetParameters real.
type ssmFalso struct {
	valores map[string]string
	err     error
	pedidos []string
}

func (f *ssmFalso) GetParameters(_ context.Context, in *ssm.GetParametersInput, _ ...func(*ssm.Options)) (*ssm.GetParametersOutput, error) {
	f.pedidos = append(f.pedidos, in.Names...)
	if f.err != nil {
		return nil, f.err
	}
	out := &ssm.GetParametersOutput{}
	for _, n := range in.Names {
		if v, ok := f.valores[n]; ok {
			out.Parameters = append(out.Parameters, types.Parameter{Name: aws.String(n), Value: aws.String(v)})
		} else {
			out.InvalidParameters = append(out.InvalidParameters, n)
		}
	}
	return out, nil
}

func TestLerParametrosSSM(t *testing.T) {
	api := &ssmFalso{valores: map[string]string{
		"/gestor-gastos/dev/events/bus-name": busTeste,
		"/gestor-gastos/dev/events/dlq-arn":  dlqTeste,
	}}
	p, err := LerParametrosSSM(context.Background(), api, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if p[ParamBusName] != busTeste || p[ParamDlqArn] != dlqTeste {
		t.Errorf("parâmetros = %v", p)
	}
	if len(api.pedidos) != 2 {
		t.Errorf("esperava uma leitura com os 2 nomes, pedidos = %v", api.pedidos)
	}
}

func TestLerParametrosSSMFaltando(t *testing.T) {
	api := &ssmFalso{valores: map[string]string{"/gestor-gastos/dev/events/bus-name": busTeste}}
	_, err := LerParametrosSSM(context.Background(), api, "dev")
	if err == nil {
		t.Fatal("esperava erro")
	}
	msg := err.Error()
	for _, trecho := range []string{
		"faltam parâmetros no SSM",
		"/gestor-gastos/dev/events/dlq-arn",
		"gestor-gastos-dev-events do gestor-gastos-infra-base",
		"gestor-gastos-dev-categories",
		"aws ssm get-parameters-by-path --path /gestor-gastos/dev",
	} {
		if !strings.Contains(msg, trecho) {
			t.Errorf("mensagem sem %q:\n%s", trecho, msg)
		}
	}
	if strings.Contains(msg, "bus-name") {
		t.Errorf("bus-name existe e não deveria aparecer:\n%s", msg)
	}
}

func TestLerParametrosSSMSemAcesso(t *testing.T) {
	_, err := LerParametrosSSM(context.Background(), &ssmFalso{err: errors.New("AccessDeniedException")}, "dev")
	if err == nil || !strings.Contains(err.Error(), "AccessDeniedException") || !strings.Contains(err.Error(), "credenciais") {
		t.Errorf("erro = %v", err)
	}
}

func appComContexto(ctx map[string]interface{}) awscdk.App {
	return awscdk.NewApp(&awscdk.AppProps{Context: &ctx})
}

func semVariaveis(string) (string, bool) { return "", false }

func naEsteira(k string) (string, bool) {
	if k == VariavelAmbienteEsteira {
		return "dev", true
	}
	return "", false
}

func TestDeveLerSsm(t *testing.T) {
	casos := []struct {
		nome   string
		ctx    map[string]interface{}
		lookup func(string) (string, bool)
		quer   bool
	}{
		{"offline", nil, semVariaveis, false},
		{"esteira", nil, naEsteira, true},
		{"contexto true", map[string]interface{}{ChaveContextoLerSsm: "true"}, semVariaveis, true},
		{"contexto bool", map[string]interface{}{ChaveContextoLerSsm: true}, semVariaveis, true},
		{"contexto false na esteira", map[string]interface{}{ChaveContextoLerSsm: "false"}, naEsteira, false},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := deveLerSsm(appComContexto(c.ctx), c.lookup); got != c.quer {
				t.Errorf("deveLerSsm = %v, quer %v", got, c.quer)
			}
		})
	}
}

func TestParametrosDoSynth(t *testing.T) {
	ctx := context.Background()
	api := &ssmFalso{valores: map[string]string{
		"/gestor-gastos/dev/events/bus-name": busTeste,
		"/gestor-gastos/dev/events/dlq-arn":  dlqTeste,
	}}
	novo := func(context.Context) (SSMAPI, error) { return api, nil }

	if p, err := parametrosDoSynth(ctx, appComContexto(nil), "dev", semVariaveis, novo); p != nil || err != nil || len(api.pedidos) != 0 {
		t.Errorf("offline: p=%v err=%v pedidos=%v", p, err, api.pedidos)
	}
	if p, err := parametrosDoSynth(ctx, appComContexto(nil), "dev", naEsteira, novo); err != nil || p[ParamBusName] != busTeste {
		t.Errorf("esteira: p=%v err=%v", p, err)
	}

	falha := func(context.Context) (SSMAPI, error) { return nil, errors.New("sem credenciais") }
	if _, err := parametrosDoSynth(ctx, appComContexto(nil), "dev", naEsteira, falha); err == nil {
		t.Error("esperava erro do client")
	}
}

func TestClienteSSM(t *testing.T) {
	// Montar o client não chama a AWS nem exige credenciais.
	if api, err := clienteSSM(context.Background()); err != nil || api == nil {
		t.Errorf("api=%v err=%v", api, err)
	}
}

func TestAmbiente(t *testing.T) {
	if env, err := Ambiente(appComContexto(nil)); env != "dev" || err != nil {
		t.Errorf("padrão: %q %v", env, err)
	}
	if _, err := Ambiente(appComContexto(map[string]interface{}{ChaveContextoAmbiente: "prod"})); err == nil {
		t.Error("prod não existe na AWS e deveria ser recusado")
	}
}

func TestAmbienteAwsUsaAContaDasCredenciais(t *testing.T) {
	t.Setenv("CDK_DEFAULT_ACCOUNT", "123456789012")
	if e := ambienteAws(); *e.Region != Regiao || e.Account == nil || *e.Account != "123456789012" {
		t.Errorf("env = %+v", e)
	}
	t.Setenv("CDK_DEFAULT_ACCOUNT", "")
	if e := ambienteAws(); e.Account != nil {
		t.Errorf("sem credenciais a conta deveria ficar vazia: %v", *e.Account)
	}
}
