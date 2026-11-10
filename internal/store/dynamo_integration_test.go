//go:build integration

// Testes contra o DynamoDB Local do infra-base (task local:up). Cada teste
// cria uma tabela própria, com o mesmo esquema do seed, e a apaga no fim,
// sem tocar na tabela gestor-gastos-categories usada no ambiente local.
//
//	go test -tags integration ./internal/store
package store

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/categoria"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/dynamotest"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func tabelaDeTeste(t *testing.T) (*Dynamo, *dynamodb.Client, string) {
	t.Helper()
	c, nome := dynamotest.CategoriesTable(t, TableName)
	d := NewDynamo(c, nome)
	if err := d.GravarPadrao(context.Background(), categoria.Padrao()); err != nil {
		t.Fatal(err)
	}
	return d, c, nome
}

var agora = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func nova(t *testing.T, id, dono, nome string, g categoria.Grupo) categoria.Categoria {
	t.Helper()
	c, err := categoria.Nova(id, dono, nome, g, "#123456", agora.Add(123*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func itensDaParticao(t *testing.T, c *dynamodb.Client, tabela, pk string) []map[string]types.AttributeValue {
	t.Helper()
	out, err := c.Query(context.Background(), &dynamodb.QueryInput{
		TableName:                 aws.String(tabela),
		KeyConditionExpression:    aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(pk)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.Items
}

func str(av types.AttributeValue) string {
	if v, ok := av.(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

func TestSeedIdempotenteNoFormatoDoInfraBase(t *testing.T) {
	ctx := context.Background()
	d, c, tabela := tabelaDeTeste(t)
	// Segunda vez não duplica.
	if err := d.GravarPadrao(ctx, categoria.Padrao()); err != nil {
		t.Fatal(err)
	}
	itens := itensDaParticao(t, c, tabela, "SYSTEM")
	if len(itens) != 12 {
		t.Fatalf("itens = %d", len(itens))
	}
	// Mesmos atributos que o seed do infra-base grava (local/seed/dynamo.go).
	for _, it := range itens {
		got := slices.Sorted(maps.Keys(it))
		if !slices.Equal(got, []string{"PK", "SK", "categoriaId", "cor", "dono", "grupo", "nome"}) {
			t.Errorf("atributos = %v", got)
		}
	}
	quer := map[string]string{
		"PK": "SYSTEM", "SK": "CAT#FIXO#agua", "categoriaId": "agua", "dono": "SYSTEM",
		"grupo": "FIXO", "nome": "Água", "cor": "#06B6D4",
	}
	i := slices.IndexFunc(itens, func(it map[string]types.AttributeValue) bool { return str(it["SK"]) == "CAT#FIXO#agua" })
	if i < 0 {
		t.Fatal("item agua não encontrado")
	}
	for k, v := range quer {
		if str(itens[i][k]) != v {
			t.Errorf("%s = %q, quer %q", k, str(itens[i][k]), v)
		}
	}

	v, err := d.Visiveis(ctx, "")
	if err != nil || len(v) != 12 {
		t.Fatalf("Visiveis = %d, %v", len(v), err)
	}
	porID := func(a, b categoria.Categoria) int { return strings.Compare(a.ID, b.ID) }
	slices.SortFunc(v, porID)
	padrao := categoria.Padrao()
	slices.SortFunc(padrao, porID)
	if !slices.Equal(v, padrao) {
		t.Errorf("padrão lidas diferem das gravadas")
	}
}

func TestCriarObterEVisiveis(t *testing.T) {
	ctx := context.Background()
	d, c, tabela := tabelaDeTeste(t)
	pet := nova(t, "c1", "u-a", "Pet", categoria.Cartao)
	if err := d.Criar(ctx, pet); err != nil {
		t.Fatal(err)
	}

	// Chaves do contrato: PK=USER#<id>, SK=CAT#<grupo>#<id>, mais a trava.
	var sks []string
	for _, it := range itensDaParticao(t, c, tabela, "USER#u-a") {
		sks = append(sks, str(it["SK"]))
	}
	slices.Sort(sks)
	if !slices.Equal(sks, []string{"CAT#CARTAO#c1", "NOME#CARTAO#pet"}) {
		t.Errorf("SKs = %v", sks)
	}

	got, err := d.Obter(ctx, "u-a", "c1")
	if err != nil || got != pet {
		t.Errorf("Obter = %+v, %v\nquer %+v", got, err, pet)
	}
	if p, err := d.Obter(ctx, "u-a", "condominio"); err != nil || !p.Padrao() || p.Grupo != categoria.Fixo {
		t.Errorf("Obter padrão = %+v, %v", p, err)
	}

	vA, _ := d.Visiveis(ctx, "u-a")
	vB, _ := d.Visiveis(ctx, "u-b")
	if len(vA) != 13 || len(vB) != 12 {
		t.Errorf("visíveis A = %d, B = %d", len(vA), len(vB))
	}
	// Isolamento: B não obtém a categoria de A.
	if _, err := d.Obter(ctx, "u-b", "c1"); !errors.Is(err, errs.NotFound(contract.CodigoNaoEncontrada, "")) {
		t.Errorf("B obtém a de A: %v", err)
	}
	if _, err := d.Obter(ctx, "u-a", "nao-existe"); !errors.Is(err, errs.NotFound(contract.CodigoNaoEncontrada, "")) {
		t.Errorf("inexistente: %v", err)
	}
}

func TestTravaDeNome(t *testing.T) {
	ctx := context.Background()
	d, _, _ := tabelaDeTeste(t)
	if err := d.Criar(ctx, nova(t, "c1", "u-a", "Pet", categoria.Cartao)); err != nil {
		t.Fatal(err)
	}
	err := d.Criar(ctx, nova(t, "c2", "u-a", "  PET ", categoria.Cartao))
	if !errors.Is(err, errs.Conflict(contract.CodigoDuplicada, "")) {
		t.Errorf("duplicada: %v", err)
	}
	// Outro grupo e outro usuário estão livres.
	if err := d.Criar(ctx, nova(t, "c3", "u-a", "Pet", categoria.Fixo)); err != nil {
		t.Error(err)
	}
	if err := d.Criar(ctx, nova(t, "c4", "u-b", "Pet", categoria.Cartao)); err != nil {
		t.Error(err)
	}
	if v, _ := d.Visiveis(ctx, "u-a"); len(v) != 14 {
		t.Errorf("visíveis = %d", len(v))
	}
}

func TestCriarConcorrente(t *testing.T) {
	ctx := context.Background()
	d, _, _ := tabelaDeTeste(t)
	var wg sync.WaitGroup
	var ok, dup atomic.Int32
	for i := range 10 {
		c := nova(t, fmt.Sprintf("c%d", i), "u-a", "Pet", categoria.Cartao)
		wg.Go(func() {
			err := d.Criar(ctx, c)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, errs.Conflict(contract.CodigoDuplicada, "")):
				dup.Add(1)
			default:
				// O DynamoDB também cancela transações concorrentes sobre o
				// mesmo item (TransactionConflict); o BFF recebe 500 e o
				// usuário tenta de novo. Não conta como sucesso.
				if !strings.Contains(err.Error(), "TransactionConflict") {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Errorf("ok = %d, duplicadas = %d", ok.Load(), dup.Load())
	}
}

func TestExcluir(t *testing.T) {
	ctx := context.Background()
	d, c, tabela := tabelaDeTeste(t)
	pet := nova(t, "c1", "u-a", "Pet", categoria.Cartao)
	if err := d.Criar(ctx, pet); err != nil {
		t.Fatal(err)
	}
	if err := d.Excluir(ctx, pet); err != nil {
		t.Fatal(err)
	}
	if n := len(itensDaParticao(t, c, tabela, "USER#u-a")); n != 0 {
		t.Errorf("sobraram %d itens (categoria ou trava)", n)
	}
	if err := d.Excluir(ctx, pet); !errors.Is(err, errs.NotFound(contract.CodigoNaoEncontrada, "")) {
		t.Errorf("segunda exclusão: %v", err)
	}
	// O nome fica livre de novo.
	if err := d.Criar(ctx, nova(t, "c2", "u-a", "pet", categoria.Cartao)); err != nil {
		t.Error(err)
	}
}

func TestExcluirDoUsuario(t *testing.T) {
	ctx := context.Background()
	d, c, tabela := tabelaDeTeste(t)
	// 30 categorias = 60 itens com as travas: mais de dois lotes de 25.
	for i := range 30 {
		g := categoria.Grupos[i%2]
		if err := d.Criar(ctx, nova(t, fmt.Sprintf("c%02d", i), "u-a", fmt.Sprintf("Cat %02d", i), g)); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Criar(ctx, nova(t, "b1", "u-b", "Pet", categoria.Cartao)); err != nil {
		t.Fatal(err)
	}

	n, err := d.ExcluirDoUsuario(ctx, "u-a")
	if err != nil || n != 30 {
		t.Fatalf("ExcluirDoUsuario = %d, %v", n, err)
	}
	if k := len(itensDaParticao(t, c, tabela, "USER#u-a")); k != 0 {
		t.Errorf("sobraram %d itens de A", k)
	}
	if k := len(itensDaParticao(t, c, tabela, "USER#u-b")); k != 2 {
		t.Errorf("B ficou com %d itens", k)
	}
	if k := len(itensDaParticao(t, c, tabela, "SYSTEM")); k != 12 {
		t.Errorf("padrão = %d", k)
	}
	// Idempotente.
	if n, err := d.ExcluirDoUsuario(ctx, "u-a"); err != nil || n != 0 {
		t.Errorf("segunda vez = %d, %v", n, err)
	}
}
