package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/categoria"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Caminhos que o DynamoDB Local não reproduz (falhas, throttling,
// paginação). O caminho feliz está nos testes de integração
// (dynamo_integration_test.go).

type fakeAPI struct {
	queryErr   error
	queryPages []*dynamodb.QueryOutput
	queryIns   []*dynamodb.QueryInput

	getErr  error
	getOuts []*dynamodb.BatchGetItemOutput
	gets    int

	writeErr  error
	writeOuts []*dynamodb.BatchWriteItemOutput
	writes    int

	txErr error
}

func (f *fakeAPI) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	cp := *in
	f.queryIns = append(f.queryIns, &cp)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if len(f.queryPages) == 0 {
		return &dynamodb.QueryOutput{}, nil
	}
	out := f.queryPages[0]
	f.queryPages = f.queryPages[1:]
	return out, nil
}

func (f *fakeAPI) BatchGetItem(context.Context, *dynamodb.BatchGetItemInput, ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error) {
	f.gets++
	if f.getErr != nil {
		return nil, f.getErr
	}
	if len(f.getOuts) == 0 {
		return &dynamodb.BatchGetItemOutput{}, nil
	}
	out := f.getOuts[0]
	if len(f.getOuts) > 1 {
		f.getOuts = f.getOuts[1:]
	}
	return out, nil
}

func (f *fakeAPI) BatchWriteItem(context.Context, *dynamodb.BatchWriteItemInput, ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	f.writes++
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	if len(f.writeOuts) == 0 {
		return &dynamodb.BatchWriteItemOutput{}, nil
	}
	out := f.writeOuts[0]
	if len(f.writeOuts) > 1 {
		f.writeOuts = f.writeOuts[1:]
	}
	return out, nil
}

func (f *fakeAPI) TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	return &dynamodb.TransactWriteItemsOutput{}, f.txErr
}

func novoStore(f *fakeAPI) *Dynamo {
	d := NewDynamo(f, "t")
	d.espera = func(int) {}
	return d
}

func pet(t *testing.T) categoria.Categoria {
	t.Helper()
	c, err := categoria.Nova("c1", "u-a", "Pet", categoria.Cartao, "#123456", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func exigeInterno(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("esperava erro interno, veio nil")
	}
	if errs.KindOf(err) != errs.KindInternal {
		t.Errorf("erro = %v, quer interno", err)
	}
}

func cancelada(codigos ...string) error {
	tce := &types.TransactionCanceledException{}
	for _, c := range codigos {
		tce.CancellationReasons = append(tce.CancellationReasons, types.CancellationReason{Code: aws.String(c)})
	}
	return tce
}

var errFalha = errors.New("ProvisionedThroughputExceededException")

func TestErrosDoDynamo(t *testing.T) {
	ctx := context.Background()

	t.Run("Query", func(t *testing.T) {
		_, err := novoStore(&fakeAPI{queryErr: errFalha}).Visiveis(ctx, "u-a")
		exigeInterno(t, err)
	})
	t.Run("BatchGetItem", func(t *testing.T) {
		_, err := novoStore(&fakeAPI{getErr: errFalha}).Obter(ctx, "u-a", "c1")
		exigeInterno(t, err)
	})
	t.Run("TransactWriteItems ao criar", func(t *testing.T) {
		exigeInterno(t, novoStore(&fakeAPI{txErr: errFalha}).Criar(ctx, pet(t)))
	})
	t.Run("ID repetido ao criar não é nome duplicado", func(t *testing.T) {
		exigeInterno(t, novoStore(&fakeAPI{txErr: cancelada("ConditionalCheckFailed", "None")}).Criar(ctx, pet(t)))
	})
	t.Run("TransactWriteItems ao excluir", func(t *testing.T) {
		exigeInterno(t, novoStore(&fakeAPI{txErr: errFalha}).Excluir(ctx, pet(t)))
	})
	t.Run("conflito de transação ao excluir", func(t *testing.T) {
		exigeInterno(t, novoStore(&fakeAPI{txErr: cancelada("None", "TransactionConflict")}).Excluir(ctx, pet(t)))
	})
	t.Run("BatchWriteItem", func(t *testing.T) {
		exigeInterno(t, novoStore(&fakeAPI{writeErr: errFalha}).GravarPadrao(ctx, categoria.Padrao()))
	})
	t.Run("Query ao excluir do usuário", func(t *testing.T) {
		_, err := novoStore(&fakeAPI{queryErr: errFalha}).ExcluirDoUsuario(ctx, "u-a")
		exigeInterno(t, err)
	})
	t.Run("BatchWriteItem ao excluir do usuário", func(t *testing.T) {
		f := &fakeAPI{writeErr: errFalha, queryPages: []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"PK": s("USER#u-a"), "SK": s("CAT#CARTAO#c1")}}}}}
		_, err := novoStore(f).ExcluirDoUsuario(ctx, "u-a")
		exigeInterno(t, err)
	})
}

func TestCondicoesDaTransacao(t *testing.T) {
	ctx := context.Background()
	err := novoStore(&fakeAPI{txErr: cancelada("None", "ConditionalCheckFailed")}).Criar(ctx, pet(t))
	if !errors.Is(err, errs.Conflict(contract.CodigoDuplicada, "")) {
		t.Errorf("Criar: %v", err)
	}
	err = novoStore(&fakeAPI{txErr: cancelada("ConditionalCheckFailed", "None")}).Excluir(ctx, pet(t))
	if !errors.Is(err, errs.NotFound(contract.CodigoNaoEncontrada, "")) {
		t.Errorf("Excluir: %v", err)
	}
	// Cancelamento sem motivos não é tratado como condição.
	exigeInterno(t, novoStore(&fakeAPI{txErr: cancelada()}).Criar(ctx, pet(t)))
}

func TestPadraoSoPeloSeed(t *testing.T) {
	ctx := context.Background()
	p := categoria.Padrao()[0]
	exigeInterno(t, novoStore(&fakeAPI{}).Criar(ctx, p))
	exigeInterno(t, novoStore(&fakeAPI{}).Excluir(ctx, p))
	exigeInterno(t, novoStore(&fakeAPI{}).GravarPadrao(ctx, []categoria.Categoria{pet(t)}))
	for _, u := range []string{"", categoria.DonoSistema} {
		_, err := novoStore(&fakeAPI{}).ExcluirDoUsuario(ctx, u)
		exigeInterno(t, err)
	}
}

func TestVisiveisPagina(t *testing.T) {
	item := func(id string) map[string]types.AttributeValue {
		return map[string]types.AttributeValue{
			"PK": s("SYSTEM"), "SK": s("CAT#CARTAO#" + id), "categoriaId": s(id), "dono": s("SYSTEM"),
			"grupo": s("CARTAO"), "nome": s(id), "cor": s("#000000"),
		}
	}
	f := &fakeAPI{queryPages: []*dynamodb.QueryOutput{
		{Items: []map[string]types.AttributeValue{item("a")}, LastEvaluatedKey: map[string]types.AttributeValue{"PK": s("SYSTEM"), "SK": s("CAT#CARTAO#a")}},
		{Items: []map[string]types.AttributeValue{item("b")}},
	}}
	cats, err := novoStore(f).Visiveis(context.Background(), "")
	if err != nil || len(cats) != 2 {
		t.Fatalf("cats = %+v, %v", cats, err)
	}
	if len(f.queryIns) != 2 || f.queryIns[1].ExclusiveStartKey == nil {
		t.Errorf("não seguiu a paginação: %d queries", len(f.queryIns))
	}
	if !aws.ToBool(f.queryIns[0].ConsistentRead) {
		t.Error("leitura deveria ser consistente")
	}
}

func TestItemCorrompido(t *testing.T) {
	ruim := map[string]types.AttributeValue{"PK": s("USER#u-a"), "SK": s("CAT#CARTAO#c1"), "categoriaId": s("c1"), "criadoEm": s("ontem")}
	tipoErrado := map[string]types.AttributeValue{"PK": s("USER#u-a"), "SK": s("CAT#CARTAO#c1"), "nome": &types.AttributeValueMemberL{Value: []types.AttributeValue{s("x")}}}
	for nome, it := range map[string]map[string]types.AttributeValue{"data": ruim, "tipo": tipoErrado} {
		t.Run(nome, func(t *testing.T) {
			_, err := novoStore(&fakeAPI{queryPages: []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{it}}}}).Visiveis(context.Background(), "u-a")
			exigeInterno(t, err)
			_, err = novoStore(&fakeAPI{getOuts: []*dynamodb.BatchGetItemOutput{{Responses: map[string][]map[string]types.AttributeValue{"t": {it}}}}}).Obter(context.Background(), "u-a", "c1")
			exigeInterno(t, err)
		})
	}
}

func TestObterRetentaChavesNaoProcessadas(t *testing.T) {
	pendente := map[string]types.KeysAndAttributes{"t": {Keys: []map[string]types.AttributeValue{{"PK": s("SYSTEM"), "SK": s("CAT#CARTAO#c1")}}}}
	achou := &dynamodb.BatchGetItemOutput{Responses: map[string][]map[string]types.AttributeValue{"t": {{
		"PK": s("SYSTEM"), "SK": s("CAT#CARTAO#c1"), "categoriaId": s("c1"), "dono": s("SYSTEM"), "grupo": s("CARTAO"), "nome": s("X"), "cor": s("#000000"),
	}}}}
	f := &fakeAPI{getOuts: []*dynamodb.BatchGetItemOutput{{UnprocessedKeys: pendente}, achou}}
	c, err := novoStore(f).Obter(context.Background(), "", "c1")
	if err != nil || c.ID != "c1" || f.gets != 2 {
		t.Errorf("Obter = %+v, %v, gets = %d", c, err, f.gets)
	}

	// Throttling que não passa: desiste com erro interno.
	f = &fakeAPI{getOuts: []*dynamodb.BatchGetItemOutput{{UnprocessedKeys: pendente}}}
	_, err = novoStore(f).Obter(context.Background(), "u-a", "c1")
	exigeInterno(t, err)
	if f.gets != maxTentativas {
		t.Errorf("gets = %d", f.gets)
	}
}

func TestLotesRetentamItensNaoProcessados(t *testing.T) {
	pendente := &dynamodb.BatchWriteItemOutput{UnprocessedItems: map[string][]types.WriteRequest{"t": {{PutRequest: &types.PutRequest{}}}}}
	f := &fakeAPI{writeOuts: []*dynamodb.BatchWriteItemOutput{pendente, {}}}
	if err := novoStore(f).GravarPadrao(context.Background(), categoria.Padrao()); err != nil || f.writes != 2 {
		t.Errorf("err = %v, writes = %d", err, f.writes)
	}

	f = &fakeAPI{writeOuts: []*dynamodb.BatchWriteItemOutput{pendente}}
	exigeInterno(t, novoStore(f).GravarPadrao(context.Background(), categoria.Padrao()))
	if f.writes != maxTentativas {
		t.Errorf("writes = %d", f.writes)
	}
}

func TestExcluirDoUsuarioEmLotes(t *testing.T) {
	var itens []map[string]types.AttributeValue
	for i := range 30 {
		sk := "CAT#CARTAO#c"
		if i%2 == 1 {
			sk = "NOME#CARTAO#n"
		}
		itens = append(itens, map[string]types.AttributeValue{"PK": s("USER#u-a"), "SK": s(sk + string(rune('a'+i)))})
	}
	f := &fakeAPI{queryPages: []*dynamodb.QueryOutput{{Items: itens}}}
	n, err := novoStore(f).ExcluirDoUsuario(context.Background(), "u-a")
	if err != nil || n != 15 || f.writes != 2 {
		t.Errorf("n = %d, err = %v, writes = %d", n, err, f.writes)
	}
	if f.queryIns[0].ProjectionExpression == nil || aws.ToString(f.queryIns[0].KeyConditionExpression) != "PK = :pk" {
		t.Errorf("query = %+v", f.queryIns[0])
	}
}

func TestEsperaPadrao(t *testing.T) {
	// A pausa real cresce com a tentativa; a primeira é curta.
	inicio := time.Now()
	NewDynamo(&fakeAPI{}, "t").espera(1)
	if d := time.Since(inicio); d > time.Second {
		t.Errorf("espera(1) = %v", d)
	}
}
