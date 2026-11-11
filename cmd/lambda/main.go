// Command lambda é a função do categories-service na AWS (stack
// gestor-gastos-<env>-categories, em infra/). Uma função só atende os três
// formatos de entrada, pelo handler.Lambda:
//
//   - o envelope do invoke, chamado pelo BFF pelo alias live;
//   - o evento UserDeleted, entregue pela regra do EventBridge;
//   - a tarefa de seed das categorias padrão, chamada pelo deploy.
//
// Variáveis de ambiente (definidas pelo CDK):
//
//	RUNTIME=aws          só roda na AWS; localmente use o cmd/local
//	ENV=dev              ambiente
//	CATEGORIES_TABLE=... tabela DynamoDB das categorias
//	LOG_LEVEL=info       opcional
//
// Build (o mesmo do go-service.yml):
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -trimpath -ldflags="-s -w" \
//	  -o dist/lambda/bootstrap ./cmd/lambda
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/handler"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/store"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/config"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/logging"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

const servico = "categories-service"

// Config é a configuração da Lambda.
type Config struct {
	Base  config.Base
	Table string
}

// LoadConfig lê o ambiente. Só aceita RUNTIME=aws e informa de uma vez todas
// as variáveis que faltam.
func LoadConfig(lookup config.LookupFunc) (Config, error) {
	base, err := config.LoadFrom(lookup, servico)
	if err != nil {
		return Config{}, err
	}
	if base.IsLocal() {
		return Config{}, errors.New("cmd/lambda exige RUNTIME=aws; localmente use o cmd/local")
	}
	r := config.NewReader(lookup)
	c := Config{Base: base, Table: r.Require("CATEGORIES_TABLE")}
	if err := r.Err(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// novoHandler monta o handler da função com o client DynamoDB da AWS.
func novoHandler(ctx context.Context, lookup config.LookupFunc) (handler.LambdaFunc, error) {
	cfg, err := LoadConfig(lookup)
	if err != nil {
		return nil, err
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Base.Region))
	if err != nil {
		return nil, fmt.Errorf("carregar configuração da AWS: %w", err)
	}
	log := logging.New(logging.Options{Service: servico, Env: cfg.Base.Env, Level: cfg.Base.LogLevel})
	svc := app.New(store.NewDynamo(dynamodb.NewFromConfig(awsCfg), cfg.Table), log)
	return handler.Lambda(svc, log), nil
}

// executar monta o handler e o entrega a iniciar (lambda.Start). Devolve o
// código de saída do processo.
func executar(ctx context.Context, lookup config.LookupFunc, iniciar func(any)) int {
	h, err := novoHandler(ctx, lookup)
	if err != nil {
		slog.Error("configuração inválida", "erro", err.Error())
		return 1
	}
	iniciar(h)
	return 0
}

func main() {
	os.Exit(executar(context.Background(), os.LookupEnv, lambda.Start))
}
