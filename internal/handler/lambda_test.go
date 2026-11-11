package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/categoria"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/memstore"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
)

func montarLambda(st *memstore.Store) LambdaFunc {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	return Lambda(app.New(st, log), log)
}

// chamar passa pela mesma serialização da AWS: payload em bytes na entrada e
// JSON na saída.
func chamar(t *testing.T, h LambdaFunc, payload string) (invoke.Response, error) {
	t.Helper()
	out, err := h(context.Background(), json.RawMessage(payload))
	if err != nil || out == nil {
		return invoke.Response{}, err
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var resp invoke.Response
	if err := json.Unmarshal(b, &resp); err != nil {
		t.Fatal(err)
	}
	return resp, nil
}

func TestLambdaSeedGravaAsPadraoEInvokeLista(t *testing.T) {
	st := memstore.New()
	h := montarLambda(st)

	out, err := h(context.Background(), json.RawMessage(`{"tarefa":"SemearPadrao"}`))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if b, _ := json.Marshal(out); string(b) != `{"status":"ok","tarefa":"SemearPadrao"}` {
		t.Errorf("seed devolveu %s", b)
	}
	if n := st.Total(categoria.DonoSistema); n != 12 {
		t.Errorf("padrão gravadas = %d, quer 12", n)
	}

	// Idempotente: rodar de novo não duplica.
	if _, err := h(context.Background(), json.RawMessage(`{"tarefa":"SemearPadrao"}`)); err != nil || st.Total(categoria.DonoSistema) != 12 {
		t.Errorf("segundo seed: err=%v total=%d", err, st.Total(categoria.DonoSistema))
	}

	resp, err := chamar(t, h, `{"operation":"ListarCategorias","userId":"u-1"}`)
	if err != nil || resp.Error != nil {
		t.Fatalf("invoke: resp=%+v err=%v", resp, err)
	}
	var pg contract.CategoriasPorGrupo
	if err := json.Unmarshal(resp.Payload, &pg); err != nil {
		t.Fatal(err)
	}
	if len(pg.Cartao) != 6 || len(pg.Fixo) != 6 {
		t.Errorf("lista = %d + %d", len(pg.Cartao), len(pg.Fixo))
	}
}

func TestLambdaTarefaDesconhecidaFalha(t *testing.T) {
	_, err := montarLambda(memstore.New())(context.Background(), json.RawMessage(`{"tarefa":"ApagarTudo"}`))
	if err == nil {
		t.Error("esperava erro")
	}
}

func TestLambdaEventoUserDeletedApagaAsPersonalizadas(t *testing.T) {
	st := memstore.ComPadrao()
	h := montarLambda(st)

	resp, err := chamar(t, h, `{"operation":"CriarCategoria","userId":"u-1","payload":{"nome":"Pet","grupo":"CARTAO","cor":"#17BEBB"}}`)
	if err != nil || resp.Error != nil || st.Total("u-1") != 1 {
		t.Fatalf("criar: resp=%+v err=%v total=%d", resp, err, st.Total("u-1"))
	}

	out, err := h(context.Background(), json.RawMessage(`{
		"version": "0", "id": "e-1", "detail-type": "UserDeleted", "source": "gestor-gastos.users",
		"account": "123456789012", "time": "2026-11-11T12:00:00Z", "region": "us-east-1", "resources": [],
		"detail": {"userId": "u-1"}
	}`))
	if err != nil || out != nil {
		t.Fatalf("evento: out=%v err=%v", out, err)
	}
	if st.Total("u-1") != 0 || st.Total(categoria.DonoSistema) != 12 {
		t.Errorf("depois do evento: u-1=%d padrão=%d", st.Total("u-1"), st.Total(categoria.DonoSistema))
	}
}

func TestLambdaInvokeDevolveErroDeDominioNoEnvelope(t *testing.T) {
	resp, err := chamar(t, montarLambda(memstore.ComPadrao()), `{"operation":"ExcluirCategoria","userId":"u-1","payload":{"id":"alimentacao"}}`)
	if err != nil {
		t.Fatalf("a função não deve falhar por erro de domínio: %v", err)
	}
	if resp.Error == nil || resp.Error.Status != 403 || resp.Error.Code != contract.CodigoPadrao {
		t.Errorf("resp = %+v", resp)
	}
}

func TestLambdaEventoComDetailInvalidoFalha(t *testing.T) {
	// O erro precisa chegar à AWS para haver retentativa e DLQ.
	_, err := montarLambda(memstore.New())(context.Background(), json.RawMessage(
		`{"id":"e-2","detail-type":"UserDeleted","source":"gestor-gastos.users","time":"2026-11-11T12:00:00Z","detail":"texto"}`))
	if err == nil {
		t.Error("esperava erro")
	}
}

func TestLambdaEventoDeOutroTipoEIgnorado(t *testing.T) {
	out, err := montarLambda(memstore.New())(context.Background(), json.RawMessage(
		`{"id":"e-3","detail-type":"OutroEvento","source":"x","time":"2026-11-11T12:00:00Z","detail":{}}`))
	if err != nil || out != nil {
		t.Errorf("out=%v err=%v", out, err)
	}
}

func TestLambdaPayloadInvalido(t *testing.T) {
	casos := map[string]string{
		"não é JSON":          `nada`,
		"não é objeto":        `[1,2]`,
		"evento malformado":   `{"detail-type":"UserDeleted","time":"ontem"}`,
		"envelope malformado": `{"operation":1}`,
		"tarefa não é texto":  `{"tarefa":1}`,
	}
	for nome, payload := range casos {
		t.Run(nome, func(t *testing.T) {
			if _, err := montarLambda(memstore.New())(context.Background(), json.RawMessage(payload)); err == nil {
				t.Error("esperava erro")
			}
		})
	}
}
