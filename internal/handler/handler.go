// Package handler liga os casos de uso aos transportes da go-commons: o
// invoke.Router (chamadas do BFF) e o events.Router (eventos). Os mesmos
// routers servem a Lambda e o cmd/local.
package handler

import (
	"context"
	"log/slog"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/categoria"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
)

// Invoke monta o router das operações do contrato.
func Invoke(svc *app.Service, log *slog.Logger) *invoke.Router {
	r := invoke.NewRouter(log)

	r.Handle(contract.OpListarCategorias, invoke.Typed(func(ctx context.Context, userID string, _ struct{}) (contract.CategoriasPorGrupo, error) {
		if err := exigirUsuario(userID); err != nil {
			return contract.CategoriasPorGrupo{}, err
		}
		pg, err := svc.ListarCategorias(ctx, userID)
		if err != nil {
			return contract.CategoriasPorGrupo{}, err
		}
		return contract.CategoriasPorGrupo{
			Cartao: paraContrato(pg[categoria.Cartao]),
			Fixo:   paraContrato(pg[categoria.Fixo]),
		}, nil
	}))

	r.Handle(contract.OpObterCategoria, invoke.Typed(func(ctx context.Context, userID string, in contract.ObterCategoriaIn) (contract.Categoria, error) {
		if err := exigirUsuario(userID); err != nil {
			return contract.Categoria{}, err
		}
		c, err := svc.ObterCategoria(ctx, userID, in.ID)
		return c.Contract(), err
	}))

	r.Handle(contract.OpCriarCategoria, invoke.Typed(func(ctx context.Context, userID string, in contract.CriarCategoriaIn) (contract.Categoria, error) {
		if err := exigirUsuario(userID); err != nil {
			return contract.Categoria{}, err
		}
		c, err := svc.CriarCategoria(ctx, userID, in.Nome, categoria.Grupo(in.Grupo), in.Cor)
		return c.Contract(), err
	}))

	r.Handle(contract.OpExcluirCategoria, invoke.Typed(func(ctx context.Context, userID string, in contract.ExcluirCategoriaIn) (any, error) {
		if err := exigirUsuario(userID); err != nil {
			return nil, err
		}
		return nil, svc.ExcluirCategoria(ctx, userID, in.ID)
	}))

	return r
}

// Events monta o router dos eventos consumidos.
func Events(svc *app.Service, log *slog.Logger) *events.Router {
	r := events.NewRouter(log)
	r.Handle(events.UserDeleted, events.Typed(func(ctx context.Context, _ events.Event, d events.UserDeletedDetail) error {
		return svc.ConsumirUserDeleted(ctx, d)
	}))
	return r
}

func paraContrato(cats []categoria.Categoria) []contract.Categoria {
	out := make([]contract.Categoria, 0, len(cats))
	for _, c := range cats {
		out = append(out, c.Contract())
	}
	return out
}

// exigirUsuario protege contra um envelope sem userId, que só acontece por
// bug no BFF: as operações sempre agem sobre o próprio usuário. "SYSTEM" é o
// dono das categorias padrão e nunca é um usuário.
func exigirUsuario(userID string) error {
	if userID == "" || userID == categoria.DonoSistema {
		return errs.BadRequest(contract.CodigoUsuarioAusente, "Requisição sem usuário.")
	}
	return nil
}
