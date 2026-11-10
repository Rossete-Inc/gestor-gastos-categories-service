package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/memstore"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadConfigPadroes(t *testing.T) {
	c, err := LoadConfig(env(map[string]string{"RUNTIME": "local"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != "8083" || c.DynamoEndpoint != "http://localhost:8000" || c.Table != "gestor-gastos-categories" || c.Base.Env != "local" || !c.SemearPadrao {
		t.Errorf("config = %+v", c)
	}
	c, err = LoadConfig(env(map[string]string{"RUNTIME": "local", "SEED_PADRAO": "false"}))
	if err != nil || c.SemearPadrao {
		t.Errorf("SEED_PADRAO=false: %+v, %v", c, err)
	}
}

func TestLoadConfigRecusa(t *testing.T) {
	casos := map[string]map[string]string{
		"sem RUNTIME":          {},
		"RUNTIME=aws":          {"RUNTIME": "aws", "ENV": "dev"},
		"DynamoDB da AWS":      {"RUNTIME": "local", "DYNAMODB_ENDPOINT": "https://dynamodb.us-east-1.amazonaws.com"},
		"endpoint sem esquema": {"RUNTIME": "local", "DYNAMODB_ENDPOINT": "localhost:8000"},
		"endpoint malformado":  {"RUNTIME": "local", "DYNAMODB_ENDPOINT": "http://[::1"},
		"SEED_PADRAO inválido": {"RUNTIME": "local", "SEED_PADRAO": "talvez"},
	}
	for nome, m := range casos {
		t.Run(nome, func(t *testing.T) {
			if _, err := LoadConfig(env(m)); err == nil {
				t.Error("esperava erro")
			}
		})
	}
	t.Run("endpoints locais aceitos", func(t *testing.T) {
		for _, e := range []string{"http://127.0.0.1:8000", "http://[::1]:8000", "http://dynamodb:8000", "http://host.docker.internal:8000"} {
			if _, err := LoadConfig(env(map[string]string{"RUNTIME": "local", "DYNAMODB_ENDPOINT": e})); err != nil {
				t.Errorf("%s: %v", e, err)
			}
		}
	})
}

func TestRunConfigInvalida(t *testing.T) {
	if err := run(context.Background(), env(map[string]string{})); err == nil {
		t.Error("esperava erro sem RUNTIME")
	}
}

func TestRunSemDynamo(t *testing.T) {
	// Porta fechada: o seed das padrão falha e o servidor nem sobe.
	err := run(context.Background(), env(map[string]string{
		"RUNTIME": "local", "LOG_LEVEL": "error", "DYNAMODB_ENDPOINT": "http://127.0.0.1:1",
	}))
	if err == nil || !strings.Contains(err.Error(), "categorias padrão") {
		t.Errorf("erro = %v", err)
	}
}

func TestRotas(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	srv := httptest.NewServer(NewHandler(app.New(memstore.ComPadrao(), log), log))
	t.Cleanup(srv.Close)

	t.Run("healthz", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || strings.TrimSpace(string(b)) != "ok" {
			t.Errorf("healthz = %d %q", resp.StatusCode, b)
		}
	})

	t.Run("invoke", func(t *testing.T) {
		var pg contract.CategoriasPorGrupo
		if err := invoke.NewClient(invoke.NewHTTP(srv.URL, nil)).Call(context.Background(), contract.OpListarCategorias, "u-1", nil, &pg); err != nil {
			t.Fatal(err)
		}
		if len(pg.Cartao) != 6 || len(pg.Fixo) != 6 {
			t.Errorf("pg = %+v", pg)
		}
		err := invoke.NewClient(invoke.NewHTTP(srv.URL, nil)).Call(context.Background(), contract.OpExcluirCategoria, "u-1", contract.ExcluirCategoriaIn{ID: "luz"}, nil)
		if e := errs.As(err); e == nil || e.Code != contract.CodigoPadrao {
			t.Errorf("erro = %v", err)
		}
	})

	t.Run("events", func(t *testing.T) {
		body := `{"id":"e1","detail-type":"UserDeleted","source":"t","detail":{"userId":"u"}}`
		resp, err := http.Post(srv.URL+"/events", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("status = %d", resp.StatusCode)
		}
	})

	t.Run("rota inexistente", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/categorias")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d", resp.StatusCode)
		}
	})
}
