package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/categoria"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/memstore"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
)

func novoService(st Store) *Service {
	s := New(st, nil)
	s.agora = func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }
	var n atomic.Int64
	s.novoID = func() string { return fmt.Sprintf("id%d", n.Add(1)) }
	return s
}

func nomes(cats []categoria.Categoria) []string {
	var out []string
	for _, c := range cats {
		out = append(out, c.Nome)
	}
	return out
}

func criar(t *testing.T, s *Service, userID, nome string, g categoria.Grupo) categoria.Categoria {
	t.Helper()
	c, err := s.CriarCategoria(context.Background(), userID, nome, g, "#123456")
	if err != nil {
		t.Fatalf("criar %q: %v", nome, err)
	}
	return c
}

// Cenário "Usuário novo".
func TestUsuarioNovoVePadraoAgrupadas(t *testing.T) {
	pg, err := novoService(memstore.ComPadrao()).ListarCategorias(context.Background(), "novo")
	if err != nil {
		t.Fatal(err)
	}
	if got := nomes(pg[categoria.Cartao]); !slices.Equal(got, []string{"Alimentação", "Compras", "Lazer", "Outros", "Saúde", "Transporte"}) {
		t.Errorf("CARTAO = %v", got)
	}
	if got := nomes(pg[categoria.Fixo]); !slices.Equal(got, []string{"Água", "Aluguel", "Condomínio", "Internet", "Luz", "Telefone"}) {
		t.Errorf("FIXO = %v", got)
	}
}

// Cenários "Criar categoria" e "Isolamento entre usuários".
func TestCriarCategoriaVisivelSoParaODono(t *testing.T) {
	ctx := context.Background()
	s := novoService(memstore.ComPadrao())
	pet := criar(t, s, "u-a", "Pet", categoria.Cartao)
	if pet.Dono != "u-a" || pet.Grupo != categoria.Cartao || pet.Padrao() || pet.ID == "" {
		t.Errorf("pet = %+v", pet)
	}

	paraA, _ := s.ListarCategorias(ctx, "u-a")
	if got := nomes(paraA[categoria.Cartao]); !slices.Equal(got, []string{"Alimentação", "Compras", "Lazer", "Outros", "Pet", "Saúde", "Transporte"}) {
		t.Errorf("A vê CARTAO = %v", got)
	}
	if len(paraA[categoria.Fixo]) != 6 {
		t.Errorf("A vê FIXO = %v", nomes(paraA[categoria.Fixo]))
	}

	paraB, _ := s.ListarCategorias(ctx, "u-b")
	if slices.Contains(nomes(paraB[categoria.Cartao]), "Pet") {
		t.Error("o Pet de A aparece para B")
	}
	if _, err := s.ObterCategoria(ctx, "u-b", pet.ID); !errors.Is(err, errs.NotFound(contract.CodigoNaoEncontrada, "")) {
		t.Errorf("B obtém a categoria de A: %v", err)
	}
	if got, err := s.ObterCategoria(ctx, "u-a", pet.ID); err != nil || got != pet {
		t.Errorf("A obtém = %+v, %v", got, err)
	}
	if got, err := s.ObterCategoria(ctx, "u-b", "alimentacao"); err != nil || !got.Padrao() {
		t.Errorf("padrão para B = %+v, %v", got, err)
	}
}

// Cenário "Nome duplicado no grupo".
func TestNomeDuplicado(t *testing.T) {
	ctx := context.Background()
	s := novoService(memstore.ComPadrao())
	criar(t, s, "u-a", "Pet", categoria.Cartao)

	for _, d := range []struct {
		nome string
		g    categoria.Grupo
	}{{"PET", categoria.Cartao}, {" pet ", categoria.Cartao}, {"lazer", categoria.Cartao}, {"LUZ", categoria.Fixo}} {
		_, err := s.CriarCategoria(ctx, "u-a", d.nome, d.g, "#123456")
		if !errors.Is(err, errs.Conflict(contract.CodigoDuplicada, "")) {
			t.Errorf("%q em %s: erro = %v", d.nome, d.g, err)
		}
	}
	// Mesmo nome em outro grupo, ou por outro usuário, é permitido.
	criar(t, s, "u-a", "Pet", categoria.Fixo)
	criar(t, s, "u-b", "Pet", categoria.Cartao)
}

// Cenário "Categoria sem grupo".
func TestCriarSemGrupo(t *testing.T) {
	st := memstore.ComPadrao()
	for _, g := range []categoria.Grupo{"", "OUTRO"} {
		_, err := novoService(st).CriarCategoria(context.Background(), "u-a", "Pet", g, "#123456")
		if e := errs.As(err); e == nil || e.Kind != errs.KindValidation || e.Fields[0].Field != "grupo" {
			t.Errorf("grupo %q: erro = %+v", g, e)
		}
	}
	if st.Total("u-a") != 0 {
		t.Error("categoria inválida chegou ao store")
	}
}

// Cenário "Categoria padrão é imutável para o usuário".
func TestExcluirPadraoRecusado(t *testing.T) {
	ctx := context.Background()
	st := memstore.ComPadrao()
	s := novoService(st)
	err := s.ExcluirCategoria(ctx, "u-a", "alimentacao")
	if !errors.Is(err, errs.Forbidden(contract.CodigoPadrao, "")) {
		t.Errorf("erro = %v", err)
	}
	if st.Total(categoria.DonoSistema) != 12 {
		t.Error("categoria padrão foi apagada")
	}
}

// Cenário "Categoria sem gastos": deixa de ser listada.
func TestExcluirPersonalizada(t *testing.T) {
	ctx := context.Background()
	s := novoService(memstore.ComPadrao())
	pet := criar(t, s, "u-a", "Pet", categoria.Cartao)

	if err := s.ExcluirCategoria(ctx, "u-a", pet.ID); err != nil {
		t.Fatal(err)
	}
	pg, _ := s.ListarCategorias(ctx, "u-a")
	if slices.Contains(nomes(pg[categoria.Cartao]), "Pet") {
		t.Error("Pet continua listada")
	}
	// Excluir de novo: 404.
	if err := s.ExcluirCategoria(ctx, "u-a", pet.ID); !errors.Is(err, errs.NotFound(contract.CodigoNaoEncontrada, "")) {
		t.Errorf("segunda exclusão: %v", err)
	}
	// Depois de excluída, o nome fica livre de novo.
	criar(t, s, "u-a", "pet", categoria.Cartao)
}

func TestExcluirDeOutroUsuario(t *testing.T) {
	ctx := context.Background()
	st := memstore.ComPadrao()
	s := novoService(st)
	pet := criar(t, s, "u-a", "Pet", categoria.Cartao)
	if err := s.ExcluirCategoria(ctx, "u-b", pet.ID); !errors.Is(err, errs.NotFound(contract.CodigoNaoEncontrada, "")) {
		t.Errorf("erro = %v", err)
	}
	if st.Total("u-a") != 1 {
		t.Error("B apagou a categoria de A")
	}
}

func TestObterIDVazioOuInexistente(t *testing.T) {
	s := novoService(memstore.ComPadrao())
	for _, id := range []string{"", "  ", "nao-existe", strings.Repeat("x", MaxID+1)} {
		if _, err := s.ObterCategoria(context.Background(), "u-a", id); !errors.Is(err, errs.NotFound(contract.CodigoNaoEncontrada, "")) {
			t.Errorf("id %q: erro = %v", id, err)
		}
	}
}

func TestDefesaContraStoreQueVazaOutroUsuario(t *testing.T) {
	// Um store com bug que devolvesse categoria alheia não pode vazá-la.
	st := memstore.ComPadrao()
	vazado := categoria.Categoria{ID: "x", Dono: "u-b", Grupo: categoria.Cartao, Nome: "Segredo", Cor: "#000000"}
	s := novoService(vazando{st, vazado})
	if _, err := s.ObterCategoria(context.Background(), "u-a", "x"); !errors.Is(err, errs.NotFound(contract.CodigoNaoEncontrada, "")) {
		t.Errorf("Obter: %v", err)
	}
	pg, _ := s.ListarCategorias(context.Background(), "u-a")
	if slices.Contains(nomes(pg[categoria.Cartao]), "Segredo") {
		t.Error("Listar vazou categoria alheia")
	}
}

type vazando struct {
	*memstore.Store
	c categoria.Categoria
}

func (v vazando) Obter(context.Context, string, string) (categoria.Categoria, error) { return v.c, nil }
func (v vazando) Visiveis(ctx context.Context, u string) ([]categoria.Categoria, error) {
	cats, err := v.Store.Visiveis(ctx, u)
	return append(cats, v.c), err
}

func TestUserDeletedIdempotente(t *testing.T) {
	ctx := context.Background()
	st := memstore.ComPadrao()
	s := novoService(st)
	criar(t, s, "u-a", "Pet", categoria.Cartao)
	criar(t, s, "u-a", "Academia", categoria.Fixo)
	criar(t, s, "u-b", "Pet", categoria.Cartao)

	// Entrega "pelo menos uma vez": o mesmo evento chega três vezes.
	for range 3 {
		if err := s.ConsumirUserDeleted(ctx, events.UserDeletedDetail{UserID: "u-a"}); err != nil {
			t.Fatal(err)
		}
	}
	if st.Total("u-a") != 0 || st.Total("u-b") != 1 || st.Total(categoria.DonoSistema) != 12 {
		t.Errorf("totais = %d/%d/%d", st.Total("u-a"), st.Total("u-b"), st.Total(categoria.DonoSistema))
	}
}

func TestUserDeletedSemUserID(t *testing.T) {
	st := memstore.ComPadrao()
	s := novoService(st)
	for _, u := range []string{"", " ", categoria.DonoSistema} {
		if err := s.ConsumirUserDeleted(context.Background(), events.UserDeletedDetail{UserID: u}); errs.KindOf(err) != errs.KindValidation {
			t.Errorf("userId %q: erro = %v", u, err)
		}
	}
	if st.Total(categoria.DonoSistema) != 12 {
		t.Error("evento sem userId apagou as padrão")
	}
}

func TestSemearPadraoIdempotente(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	s := novoService(st)
	for range 2 {
		if err := s.SemearPadrao(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if st.Total(categoria.DonoSistema) != 12 {
		t.Errorf("padrão = %d", st.Total(categoria.DonoSistema))
	}
}

func TestErrosDoStore(t *testing.T) {
	ctx := context.Background()
	st := memstore.ComPadrao()
	s := novoService(st)
	pet := criar(t, s, "u-a", "Pet", categoria.Cartao)
	st.Err = errs.Internal(errors.New("dynamodb fora"))

	chamadas := map[string]func() error{
		"Listar":     func() error { _, err := s.ListarCategorias(ctx, "u-a"); return err },
		"Obter":      func() error { _, err := s.ObterCategoria(ctx, "u-a", pet.ID); return err },
		"Criar":      func() error { _, err := s.CriarCategoria(ctx, "u-a", "Nova", categoria.Cartao, "#123456"); return err },
		"Excluir":    func() error { return s.ExcluirCategoria(ctx, "u-a", pet.ID) },
		"UserDelete": func() error { return s.ConsumirUserDeleted(ctx, events.UserDeletedDetail{UserID: "u-a"}) },
		"Semear":     func() error { return s.SemearPadrao(ctx) },
	}
	for nome, f := range chamadas {
		if err := f(); errs.KindOf(err) != errs.KindInternal {
			t.Errorf("%s: erro = %v", nome, err)
		}
	}
}

// falhaNaGravacao deixa ler mas falha ao gravar ou excluir, para cobrir o
// erro depois das checagens.
type falhaNaGravacao struct{ *memstore.Store }

func (falhaNaGravacao) Criar(context.Context, categoria.Categoria) error {
	return errs.Internal(errors.New("falha"))
}
func (falhaNaGravacao) Excluir(context.Context, categoria.Categoria) error {
	return errs.Internal(errors.New("falha"))
}

func TestErroAoGravar(t *testing.T) {
	ctx := context.Background()
	st := memstore.ComPadrao()
	st.Gravar(categoria.Categoria{ID: "c1", Dono: "u-a", Grupo: categoria.Cartao, Nome: "Pet", Cor: "#000000"})
	s := novoService(falhaNaGravacao{st})
	if _, err := s.CriarCategoria(ctx, "u-a", "Nova", categoria.Cartao, "#123456"); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("Criar: %v", err)
	}
	if err := s.ExcluirCategoria(ctx, "u-a", "c1"); errs.KindOf(err) != errs.KindInternal {
		t.Errorf("Excluir: %v", err)
	}
}

func TestCriarConcorrenteMesmoNome(t *testing.T) {
	ctx := context.Background()
	st := memstore.ComPadrao()
	s := novoService(st)
	var wg sync.WaitGroup
	var ok, dup atomic.Int32
	for range 20 {
		wg.Go(func() {
			_, err := s.CriarCategoria(ctx, "u-a", "Pet", categoria.Cartao, "#123456")
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, errs.Conflict(contract.CodigoDuplicada, "")):
				dup.Add(1)
			default:
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if ok.Load() != 1 || dup.Load() != 19 || st.Total("u-a") != 1 {
		t.Errorf("ok = %d, duplicadas = %d, total = %d", ok.Load(), dup.Load(), st.Total("u-a"))
	}
}

func TestNovoIDPadrao(t *testing.T) {
	a, b := novoID(), novoID()
	if len(a) != 24 || a == b {
		t.Errorf("ids = %q, %q", a, b)
	}
	if New(memstore.New(), nil).log == nil {
		t.Error("log nil deveria usar o padrão")
	}
}
