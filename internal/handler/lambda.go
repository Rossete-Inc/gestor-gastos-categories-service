package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
)

// TarefaSemearPadrao é o payload {"tarefa":"SemearPadrao"} que o custom
// resource do deploy (infra/categories.go) manda para gravar as categorias
// padrão. Igual a TarefaSemearPadrao do infra/config.go.
const TarefaSemearPadrao = "SemearPadrao"

// LambdaFunc é a assinatura passada a lambda.Start.
type LambdaFunc func(ctx context.Context, raw json.RawMessage) (any, error)

// Lambda devolve o handler único da função na AWS. A mesma função recebe três
// formatos:
//
//   - o envelope do invoke, chamado pelo BFF de forma síncrona pelo alias live;
//   - o evento do EventBridge, entregue de forma assíncrona pela regra de
//     UserDeleted (reconhecido pelo campo "detail-type");
//   - a tarefa de seed {"tarefa":"SemearPadrao"}, chamada pelo deploy na
//     versão nova (reconhecida pelo campo "tarefa").
//
// No invoke, erros de domínio voltam dentro do envelope e a função não falha.
// No evento e na tarefa, o erro é devolvido para a AWS: no evento há
// retentativa e DLQ; na tarefa, o erro fica no log e no alarme de erros.
func Lambda(svc *app.Service, log *slog.Logger) LambdaFunc {
	inv := Invoke(svc, log)
	ev := Events(svc, log)
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var sonda struct {
			DetailType *string `json:"detail-type"`
			Tarefa     *string `json:"tarefa"`
		}
		if err := json.Unmarshal(raw, &sonda); err != nil {
			return nil, fmt.Errorf("payload não é um objeto JSON: %w", err)
		}
		switch {
		case sonda.Tarefa != nil:
			if *sonda.Tarefa != TarefaSemearPadrao {
				return nil, fmt.Errorf("tarefa desconhecida: %q", *sonda.Tarefa)
			}
			if err := svc.SemearPadrao(ctx); err != nil {
				return nil, fmt.Errorf("semear categorias padrão: %w", err)
			}
			return map[string]string{"tarefa": TarefaSemearPadrao, "status": "ok"}, nil
		case sonda.DetailType != nil:
			var e events.Event
			if err := json.Unmarshal(raw, &e); err != nil {
				return nil, fmt.Errorf("evento inválido: %w", err)
			}
			return nil, ev.Dispatch(ctx, e)
		}
		var req invoke.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("envelope do invoke inválido: %w", err)
		}
		return inv.Dispatch(ctx, req), nil
	}
}
