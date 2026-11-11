package main

import (
	"context"
	"testing"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/handler"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

var envValido = map[string]string{"RUNTIME": "aws", "ENV": "dev", "CATEGORIES_TABLE": "gestor-gastos-dev-categories"}

func TestLoadConfig(t *testing.T) {
	c, err := LoadConfig(env(envValido))
	if err != nil {
		t.Fatal(err)
	}
	if c.Table != "gestor-gastos-dev-categories" || c.Base.Env != "dev" || c.Base.Region != "us-east-1" {
		t.Errorf("config = %+v", c)
	}
}

func TestLoadConfigRecusa(t *testing.T) {
	casos := map[string]map[string]string{
		"sem RUNTIME":          {"ENV": "dev", "CATEGORIES_TABLE": "t"},
		"RUNTIME=local":        {"RUNTIME": "local", "CATEGORIES_TABLE": "t"},
		"sem CATEGORIES_TABLE": {"RUNTIME": "aws", "ENV": "dev"},
		"sem ENV":              {"RUNTIME": "aws", "CATEGORIES_TABLE": "t"},
	}
	for nome, m := range casos {
		t.Run(nome, func(t *testing.T) {
			if _, err := LoadConfig(env(m)); err == nil {
				t.Error("esperava erro")
			}
		})
	}
}

func TestExecutarEntregaOHandler(t *testing.T) {
	var recebido any
	if codigo := executar(context.Background(), env(envValido), func(h any) { recebido = h }); codigo != 0 {
		t.Fatalf("código = %d", codigo)
	}
	if _, ok := recebido.(handler.LambdaFunc); !ok {
		t.Errorf("handler = %T", recebido)
	}
}

func TestExecutarFalhaSemConfig(t *testing.T) {
	chamou := false
	if codigo := executar(context.Background(), env(map[string]string{}), func(any) { chamou = true }); codigo != 1 || chamou {
		t.Errorf("código = %d, iniciou = %v", codigo, chamou)
	}
}
