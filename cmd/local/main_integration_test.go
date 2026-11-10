//go:build integration

package main

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/dynamotest"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
)

func portaLivre(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	_, porta, _ := net.SplitHostPort(l.Addr().String())
	return porta
}

func nomes(cats []contract.Categoria) string {
	var n []string
	for _, c := range cats {
		n = append(n, c.Nome)
	}
	return strings.Join(n, ",")
}

// TestPontaAPonta sobe o cmd/local de verdade, contra uma tabela temporária
// no DynamoDB Local, e o usa como o BFF e o publisher de eventos usarão. É o
// roteiro da validação local da fatia (tarefa 7.5) do lado do serviço.
func TestPontaAPonta(t *testing.T) {
	_, tabela := dynamotest.CategoriesTable(t, "gestor-gastos-categories")
	porta := portaLivre(t)
	base := "http://127.0.0.1:" + porta

	ctx, cancel := context.WithCancel(context.Background())
	terminou := make(chan error, 1)
	go func() {
		terminou <- run(ctx, env(map[string]string{
			"RUNTIME": "local", "PORT": porta, "LOG_LEVEL": "error",
			"DYNAMODB_ENDPOINT": dynamotest.Endpoint(), "CATEGORIES_TABLE": tabela,
		}))
	}()

	pronto := false
	for range 50 {
		if resp, err := http.Get(base + "/healthz"); err == nil {
			_ = resp.Body.Close()
			pronto = resp.StatusCode == http.StatusOK
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !pronto {
		cancel()
		t.Fatalf("cmd/local não subiu: %v", <-terminou)
	}

	bff := invoke.NewClient(invoke.NewHTTP(base, nil))
	listar := func(t *testing.T, u string) contract.CategoriasPorGrupo {
		t.Helper()
		var pg contract.CategoriasPorGrupo
		if err := bff.Call(ctx, contract.OpListarCategorias, u, nil, &pg); err != nil {
			t.Fatal(err)
		}
		return pg
	}

	t.Run("usuário novo vê as padrão semeadas ao subir", func(t *testing.T) {
		pg := listar(t, "u-a")
		if nomes(pg.Cartao) != "Alimentação,Compras,Lazer,Outros,Saúde,Transporte" ||
			nomes(pg.Fixo) != "Água,Aluguel,Condomínio,Internet,Luz,Telefone" {
			t.Errorf("pg = %+v", pg)
		}
	})

	var pet contract.Categoria
	t.Run("criar, listar e obter", func(t *testing.T) {
		if err := bff.Call(ctx, contract.OpCriarCategoria, "u-a", contract.CriarCategoriaIn{Nome: "Pet", Grupo: "CARTAO", Cor: "#E4572E"}, &pet); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(nomes(listar(t, "u-a").Cartao), "Pet") {
			t.Error("Pet não aparece para o dono")
		}
		if strings.Contains(nomes(listar(t, "u-b").Cartao), "Pet") {
			t.Error("Pet aparece para outro usuário")
		}
		var obtida contract.Categoria
		if err := bff.Call(ctx, contract.OpObterCategoria, "u-a", contract.ObterCategoriaIn{ID: pet.ID}, &obtida); err != nil || obtida != pet {
			t.Errorf("obtida = %+v, %v", obtida, err)
		}
	})

	t.Run("nome duplicado", func(t *testing.T) {
		err := bff.Call(ctx, contract.OpCriarCategoria, "u-a", contract.CriarCategoriaIn{Nome: "pET", Grupo: "CARTAO", Cor: "#000000"}, nil)
		if e := errs.As(err); e == nil || e.Code != contract.CodigoDuplicada {
			t.Errorf("erro = %v", err)
		}
	})

	t.Run("padrão é imutável", func(t *testing.T) {
		err := bff.Call(ctx, contract.OpExcluirCategoria, "u-a", contract.ExcluirCategoriaIn{ID: "alimentacao"}, nil)
		if e := errs.As(err); e == nil || e.Code != contract.CodigoPadrao {
			t.Errorf("erro = %v", err)
		}
	})

	t.Run("excluir personalizada", func(t *testing.T) {
		if err := bff.Call(ctx, contract.OpExcluirCategoria, "u-a", contract.ExcluirCategoriaIn{ID: pet.ID}, nil); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(nomes(listar(t, "u-a").Cartao), "Pet") {
			t.Error("Pet continua listada")
		}
	})

	t.Run("UserDeleted pelo publisher local", func(t *testing.T) {
		if err := bff.Call(ctx, contract.OpCriarCategoria, "u-c", contract.CriarCategoriaIn{Nome: "Academia", Grupo: "FIXO", Cor: "#000000"}, nil); err != nil {
			t.Fatal(err)
		}
		pub := events.NewLocal([]string{base + events.Path}, "gestor-gastos.teste", nil, nil)
		for range 2 {
			if err := pub.Publish(ctx, events.UserDeleted, events.UserDeletedDetail{UserID: "u-c"}); err != nil {
				t.Fatal(err)
			}
		}
		pg := listar(t, "u-c")
		if len(pg.Fixo) != 6 || strings.Contains(nomes(pg.Fixo), "Academia") {
			t.Errorf("FIXO = %s", nomes(pg.Fixo))
		}
	})

	cancel()
	select {
	case err := <-terminou:
		if err != nil {
			t.Errorf("encerramento: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("cmd/local não encerrou")
	}
}
