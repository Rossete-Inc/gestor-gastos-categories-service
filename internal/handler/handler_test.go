package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/memstore"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
)

// transporte entrega o envelope direto ao router, passando por JSON como os
// transportes reais.
type transporte struct{ r *invoke.Router }

func (t transporte) Send(ctx context.Context, req invoke.Request) (invoke.Response, error) {
	b, _ := json.Marshal(req)
	var dec invoke.Request
	_ = json.Unmarshal(b, &dec)
	resp := t.r.Dispatch(ctx, dec)
	b, _ = json.Marshal(resp)
	var out invoke.Response
	_ = json.Unmarshal(b, &out)
	return out, nil
}

func montar() (*invoke.Client, *events.Router, *memstore.Store) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	st := memstore.ComPadrao()
	svc := app.New(st, log)
	return invoke.NewClient(transporte{Invoke(svc, log)}), Events(svc, log), st
}

func nomes(cats []contract.Categoria) string {
	var n []string
	for _, c := range cats {
		n = append(n, c.Nome)
	}
	return strings.Join(n, ",")
}

func TestListarUsuarioNovo(t *testing.T) {
	c, _, _ := montar()
	var pg contract.CategoriasPorGrupo
	if err := c.Call(context.Background(), contract.OpListarCategorias, "u-1", nil, &pg); err != nil {
		t.Fatal(err)
	}
	if got := nomes(pg.Cartao); got != "Alimentação,Compras,Lazer,Outros,Saúde,Transporte" {
		t.Errorf("CARTAO = %s", got)
	}
	if got := nomes(pg.Fixo); got != "Água,Aluguel,Condomínio,Internet,Luz,Telefone" {
		t.Errorf("FIXO = %s", got)
	}
	for _, cat := range append(pg.Cartao, pg.Fixo...) {
		if !cat.Padrao || cat.ID == "" || cat.Cor == "" {
			t.Errorf("categoria = %+v", cat)
		}
	}
}

func TestListarFormatoJSON(t *testing.T) {
	// Os grupos vazios saem como [] e não null, como pede o openapi.
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	r := Invoke(app.New(memstore.New(), log), log)
	resp := r.Dispatch(context.Background(), invoke.Request{Operation: contract.OpListarCategorias, UserID: "u-1"})
	if string(resp.Payload) != `{"CARTAO":[],"FIXO":[]}` {
		t.Errorf("payload = %s", resp.Payload)
	}
}

func TestCriarObterExcluirPeloContrato(t *testing.T) {
	ctx := context.Background()
	c, _, _ := montar()

	var criada contract.Categoria
	in := contract.CriarCategoriaIn{Nome: " Pet ", Grupo: contract.GrupoCartao, Cor: "#e4572e"}
	if err := c.Call(ctx, contract.OpCriarCategoria, "u-1", in, &criada); err != nil {
		t.Fatal(err)
	}
	if criada.ID == "" || criada.Nome != "Pet" || criada.Grupo != "CARTAO" || criada.Cor != "#E4572E" || criada.Padrao {
		t.Errorf("criada = %+v", criada)
	}

	var obtida contract.Categoria
	if err := c.Call(ctx, contract.OpObterCategoria, "u-1", contract.ObterCategoriaIn{ID: criada.ID}, &obtida); err != nil || obtida != criada {
		t.Errorf("obtida = %+v, %v", obtida, err)
	}

	var pg contract.CategoriasPorGrupo
	_ = c.Call(ctx, contract.OpListarCategorias, "u-1", nil, &pg)
	if !strings.Contains(nomes(pg.Cartao), "Pet") {
		t.Errorf("Pet não listada: %s", nomes(pg.Cartao))
	}
	var outro contract.CategoriasPorGrupo
	_ = c.Call(ctx, contract.OpListarCategorias, "u-2", nil, &outro)
	if strings.Contains(nomes(outro.Cartao), "Pet") {
		t.Error("Pet aparece para outro usuário")
	}

	if err := c.Call(ctx, contract.OpExcluirCategoria, "u-1", contract.ExcluirCategoriaIn{ID: criada.ID}, nil); err != nil {
		t.Fatal(err)
	}
	err := c.Call(ctx, contract.OpObterCategoria, "u-1", contract.ObterCategoriaIn{ID: criada.ID}, nil)
	if e := errs.As(err); e == nil || e.Kind != errs.KindNotFound || e.Code != contract.CodigoNaoEncontrada {
		t.Errorf("depois de excluir: %+v", e)
	}
}

func TestErrosPeloContrato(t *testing.T) {
	ctx := context.Background()
	c, _, _ := montar()
	_ = c.Call(ctx, contract.OpCriarCategoria, "u-1", contract.CriarCategoriaIn{Nome: "Pet", Grupo: "CARTAO", Cor: "#000000"}, nil)

	casos := []struct {
		nome   string
		op     string
		in     any
		kind   errs.Kind
		codigo string
		status int
	}{
		{"duplicada", contract.OpCriarCategoria, contract.CriarCategoriaIn{Nome: "PET", Grupo: "CARTAO", Cor: "#000000"}, errs.KindConflict, contract.CodigoDuplicada, 409},
		{"duplicada com padrão", contract.OpCriarCategoria, contract.CriarCategoriaIn{Nome: "água", Grupo: "FIXO", Cor: "#000000"}, errs.KindConflict, contract.CodigoDuplicada, 409},
		{"sem grupo", contract.OpCriarCategoria, contract.CriarCategoriaIn{Nome: "Pet", Cor: "#000000"}, errs.KindValidation, "validacao", 422},
		{"excluir padrão", contract.OpExcluirCategoria, contract.ExcluirCategoriaIn{ID: "alimentacao"}, errs.KindForbidden, contract.CodigoPadrao, 403},
		{"excluir inexistente", contract.OpExcluirCategoria, contract.ExcluirCategoriaIn{ID: "x"}, errs.KindNotFound, contract.CodigoNaoEncontrada, 404},
		{"obter inexistente", contract.OpObterCategoria, contract.ObterCategoriaIn{ID: "x"}, errs.KindNotFound, contract.CodigoNaoEncontrada, 404},
		{"payload inválido", contract.OpCriarCategoria, "texto", errs.KindBadRequest, "payload_invalido", 400},
	}
	for _, cs := range casos {
		t.Run(cs.nome, func(t *testing.T) {
			err := c.Call(ctx, cs.op, "u-1", cs.in, nil)
			e := errs.As(err)
			if e == nil || e.Kind != cs.kind || e.Code != cs.codigo || e.Kind.Status() != cs.status {
				t.Errorf("erro = %+v", e)
			}
		})
	}
}

func TestValidacaoTrazCampos(t *testing.T) {
	c, _, _ := montar()
	err := c.Call(context.Background(), contract.OpCriarCategoria, "u-1", contract.CriarCategoriaIn{Nome: "", Grupo: "OUTRO", Cor: "azul"}, nil)
	e := errs.As(err)
	if e == nil || len(e.Fields) != 3 {
		t.Fatalf("erro = %+v", e)
	}
	for i, campo := range []string{"nome", "grupo", "cor"} {
		if e.Fields[i].Field != campo {
			t.Errorf("campo %d = %s", i, e.Fields[i].Field)
		}
	}
}

func TestSemUsuario(t *testing.T) {
	c, _, _ := montar()
	ops := []string{contract.OpListarCategorias, contract.OpObterCategoria, contract.OpCriarCategoria, contract.OpExcluirCategoria}
	for _, u := range []string{"", "SYSTEM"} {
		for _, op := range ops {
			err := c.Call(context.Background(), op, u, nil, nil)
			if e := errs.As(err); e == nil || e.Code != contract.CodigoUsuarioAusente {
				t.Errorf("%s com %q: erro = %+v", op, u, e)
			}
		}
	}
}

func TestErroInternoNaoVaza(t *testing.T) {
	c, _, st := montar()
	st.Err = errs.Internal(io.ErrUnexpectedEOF)
	err := c.Call(context.Background(), contract.OpListarCategorias, "u-1", nil, nil)
	e := errs.As(err)
	if e == nil || e.Kind != errs.KindInternal || strings.Contains(e.Message, "EOF") {
		t.Errorf("erro = %+v", e)
	}
}

func TestEventoUserDeleted(t *testing.T) {
	ctx := context.Background()
	c, ev, st := montar()
	_ = c.Call(ctx, contract.OpCriarCategoria, "u-7", contract.CriarCategoriaIn{Nome: "Pet", Grupo: "CARTAO", Cor: "#000000"}, nil)

	detail, _ := json.Marshal(events.UserDeletedDetail{UserID: "u-7"})
	e := events.Event{ID: "e-1", DetailType: events.UserDeleted, Source: "gestor-gastos.users-service", Time: time.Now(), Detail: detail}
	for range 2 {
		if err := ev.LambdaHandler()(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if st.Total("u-7") != 0 || st.Total("SYSTEM") != 12 {
		t.Errorf("totais = %d/%d", st.Total("u-7"), st.Total("SYSTEM"))
	}
}

func TestEventoInvalido(t *testing.T) {
	_, ev, _ := montar()
	// Detail sem userId vira erro e, na AWS, vai para a DLQ depois das
	// retentativas.
	err := ev.Dispatch(context.Background(), events.Event{DetailType: events.UserDeleted, Detail: json.RawMessage(`{}`)})
	if errs.KindOf(err) != errs.KindValidation {
		t.Errorf("erro = %v", err)
	}
	// Outros eventos são ignorados.
	if err := ev.Dispatch(context.Background(), events.Event{DetailType: events.UserConfirmed, Detail: json.RawMessage(`{}`)}); err != nil {
		t.Errorf("UserConfirmed: %v", err)
	}
}
