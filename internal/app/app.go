// Package app tem os casos de uso do categories-service. Não conhece
// transporte (invoke, eventos) nem banco: recebe um Store.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/categoria"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
)

// Store persiste categorias.
type Store interface {
	// Visiveis devolve as categorias padrão e as personalizadas do usuário.
	Visiveis(ctx context.Context, userID string) ([]categoria.Categoria, error)
	// Obter devolve a categoria id se for padrão ou do usuário, ou
	// errs.NotFound("categoria_nao_encontrada").
	Obter(ctx context.Context, userID, id string) (categoria.Categoria, error)
	// Criar grava a categoria personalizada. Se o usuário já tiver outra com o
	// mesmo nome no grupo (inclusive por uma criação simultânea), devolve
	// errs.Conflict("categoria_duplicada").
	Criar(ctx context.Context, c categoria.Categoria) error
	// Excluir apaga a categoria personalizada, ou devolve
	// errs.NotFound("categoria_nao_encontrada") se ela já não existir.
	Excluir(ctx context.Context, c categoria.Categoria) error
	// ExcluirDoUsuario apaga todas as categorias personalizadas do usuário e
	// devolve quantas apagou. Sem nada para apagar, devolve 0 sem erro.
	ExcluirDoUsuario(ctx context.Context, userID string) (int, error)
	// GravarPadrao grava (ou regrava) as categorias padrão.
	GravarPadrao(ctx context.Context, cats []categoria.Categoria) error
}

// MaxID é o tamanho máximo aceito para um ID de categoria.
const MaxID = 64

// Service implementa os casos de uso.
type Service struct {
	store  Store
	log    *slog.Logger
	agora  func() time.Time
	novoID func() string
}

// New cria o Service. log nil usa slog.Default().
func New(store Store, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: store, log: log, agora: time.Now, novoID: novoID}
}

// novoID gera 96 bits aleatórios em hexadecimal (24 caracteres). Só tem
// dígitos e a-f, então nunca coincide com o ID de uma categoria padrão.
func novoID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ListarCategorias devolve as categorias visíveis ao usuário, por grupo e em
// ordem alfabética.
func (s *Service) ListarCategorias(ctx context.Context, userID string) (map[categoria.Grupo][]categoria.Categoria, error) {
	cats, err := s.store.Visiveis(ctx, userID)
	if err != nil {
		return nil, err
	}
	// O store já filtra pela partition key; o filtro aqui é defesa extra.
	return categoria.PorGrupo(categoria.Visiveis(cats, userID)), nil
}

// ObterCategoria devolve uma categoria visível ao usuário.
func (s *Service) ObterCategoria(ctx context.Context, userID, id string) (categoria.Categoria, error) {
	id = strings.TrimSpace(id)
	// Nenhum ID passa de 64 caracteres (openapi: IdPath); um maior nem chega
	// ao banco, que recusaria a chave.
	if id == "" || len(id) > MaxID {
		return categoria.Categoria{}, categoria.ErrNaoEncontrada()
	}
	c, err := s.store.Obter(ctx, userID, id)
	if err != nil {
		return categoria.Categoria{}, err
	}
	if !c.VisivelPara(userID) {
		return categoria.Categoria{}, categoria.ErrNaoEncontrada()
	}
	return c, nil
}

// CriarCategoria cria uma categoria personalizada. O nome não pode repetir,
// ignorando maiúsculas e minúsculas, o de outra visível no mesmo grupo,
// padrão ou do próprio usuário. Entre categorias do usuário, o store garante
// a regra mesmo com criações simultâneas.
func (s *Service) CriarCategoria(ctx context.Context, userID, nome string, grupo categoria.Grupo, cor string) (categoria.Categoria, error) {
	c, err := categoria.Nova(s.novoID(), userID, nome, grupo, cor, s.agora())
	if err != nil {
		return categoria.Categoria{}, err
	}
	visiveis, err := s.store.Visiveis(ctx, userID)
	if err != nil {
		return categoria.Categoria{}, err
	}
	if err := categoria.ExigirNomeDisponivel(visiveis, c); err != nil {
		return categoria.Categoria{}, err
	}
	if err := s.store.Criar(ctx, c); err != nil {
		return categoria.Categoria{}, err
	}
	s.log.InfoContext(ctx, "categoria criada", "userId", userID, "categoriaId", c.ID, "grupo", c.Grupo)
	return c, nil
}

// ExcluirCategoria exclui uma categoria personalizada do usuário. Padrão é
// recusada com 403; a checagem de gastos vinculados é feita antes, pelo BFF.
func (s *Service) ExcluirCategoria(ctx context.Context, userID, id string) error {
	c, err := s.ObterCategoria(ctx, userID, id)
	if err != nil {
		return err
	}
	if err := c.ExigirExcluivel(userID); err != nil {
		return err
	}
	if err := s.store.Excluir(ctx, c); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "categoria excluída", "userId", userID, "categoriaId", c.ID)
	return nil
}

// ConsumirUserDeleted apaga as categorias personalizadas do usuário excluído.
// É idempotente: o mesmo evento de novo não encontra nada e não falha.
func (s *Service) ConsumirUserDeleted(ctx context.Context, d events.UserDeletedDetail) error {
	userID := strings.TrimSpace(d.UserID)
	if userID == "" || userID == categoria.DonoSistema {
		return errs.Validation("Evento UserDeleted sem userId.", errs.FieldError{Field: "userId", Message: "obrigatório"})
	}
	n, err := s.store.ExcluirDoUsuario(ctx, userID)
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "categorias do usuário excluído apagadas", "userId", userID, "quantidade", n)
	return nil
}

// SemearPadrao grava as categorias padrão. É idempotente e também corrige
// nome ou cor de uma padrão que tenha mudado na fonte da verdade.
func (s *Service) SemearPadrao(ctx context.Context) error {
	cats := categoria.Padrao()
	if err := s.store.GravarPadrao(ctx, cats); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "categorias padrão gravadas", "quantidade", len(cats))
	return nil
}
