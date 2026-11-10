// Package store grava as categorias no DynamoDB.
//
// Chaves (contrato com o CDK e com o seed do infra-base, docs/local.md):
//
//	categoria padrão          PK=SYSTEM       SK=CAT#<grupo>#<id>
//	categoria personalizada   PK=USER#<id>    SK=CAT#<grupo>#<id>
//	trava de nome             PK=USER#<id>    SK=NOME#<grupo>#<nome em minúsculas>
//
// A trava de nome é gravada na mesma transação da categoria e garante o nome
// único por grupo entre as categorias do usuário, mesmo com duas criações
// simultâneas. Ela fica na partição do usuário, então some junto quando o
// usuário é excluído, e não aparece nas listagens, que filtram SK por CAT#.
// A colisão com o nome de uma categoria padrão é checada pelo app, porque as
// padrão só mudam pelo seed.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/categoria"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// TableName é o nome base da tabela; na AWS o CDK acrescenta o ambiente.
const TableName = "gestor-gastos-categories"

const (
	pkSistema   = "SYSTEM"
	prefixoCat  = "CAT#"
	formatoData = "2006-01-02T15:04:05.000Z"
	// maxLote é o limite de itens do BatchWriteItem.
	maxLote = 25
	// maxTentativas limita as retentativas de itens não processados pelos
	// lotes (throttling).
	maxTentativas = 8
)

// API é o subconjunto do client DynamoDB usado aqui.
type API interface {
	Query(ctx context.Context, in *dynamodb.QueryInput, opt ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
	BatchGetItem(ctx context.Context, in *dynamodb.BatchGetItemInput, opt ...func(*dynamodb.Options)) (*dynamodb.BatchGetItemOutput, error)
	BatchWriteItem(ctx context.Context, in *dynamodb.BatchWriteItemInput, opt ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error)
	TransactWriteItems(ctx context.Context, in *dynamodb.TransactWriteItemsInput, opt ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

// Dynamo implementa app.Store.
type Dynamo struct {
	api    API
	table  string
	espera func(tentativa int) // pausa entre retentativas; trocada nos testes
}

// NewDynamo cria o store na tabela table.
func NewDynamo(api API, table string) *Dynamo {
	return &Dynamo{api: api, table: table, espera: func(n int) { time.Sleep(time.Duration(n*n) * 20 * time.Millisecond) }}
}

// item tem os mesmos atributos que o seed do infra-base grava nas padrão.
type item struct {
	PK          string `dynamodbav:"PK"`
	SK          string `dynamodbav:"SK"`
	CategoriaID string `dynamodbav:"categoriaId"`
	Dono        string `dynamodbav:"dono"`
	Grupo       string `dynamodbav:"grupo"`
	Nome        string `dynamodbav:"nome"`
	Cor         string `dynamodbav:"cor"`
	CriadoEm    string `dynamodbav:"criadoEm,omitempty"`
}

// trava é o item que reserva o nome no grupo.
type trava struct {
	PK          string `dynamodbav:"PK"`
	SK          string `dynamodbav:"SK"`
	CategoriaID string `dynamodbav:"categoriaId"`
}

func pk(dono string) string {
	if dono == categoria.DonoSistema {
		return pkSistema
	}
	return "USER#" + dono
}

func skCat(g categoria.Grupo, id string) string { return prefixoCat + string(g) + "#" + id }

// skNome usa categoria.ChaveNome. Mudar a regra da chave exige migrar as
// travas existentes.
func skNome(g categoria.Grupo, nome string) string {
	return "NOME#" + string(g) + "#" + categoria.ChaveNome(nome)
}

func paraItem(c categoria.Categoria) item {
	it := item{
		PK: pk(c.Dono), SK: skCat(c.Grupo, c.ID),
		CategoriaID: c.ID, Dono: c.Dono, Grupo: string(c.Grupo), Nome: c.Nome, Cor: c.Cor,
	}
	if !c.CriadoEm.IsZero() {
		it.CriadoEm = c.CriadoEm.UTC().Format(formatoData)
	}
	return it
}

func (it item) categoria() (categoria.Categoria, error) {
	c := categoria.Categoria{ID: it.CategoriaID, Dono: it.Dono, Grupo: categoria.Grupo(it.Grupo), Nome: it.Nome, Cor: it.Cor}
	if it.CriadoEm != "" {
		t, err := time.Parse(formatoData, it.CriadoEm)
		if err != nil {
			return categoria.Categoria{}, fmt.Errorf("criadoEm %q: %w", it.CriadoEm, err)
		}
		c.CriadoEm = t
	}
	return c, nil
}

func decodificar(raw map[string]types.AttributeValue) (categoria.Categoria, error) {
	var it item
	if err := attributevalue.UnmarshalMap(raw, &it); err != nil {
		return categoria.Categoria{}, errs.Internal(fmt.Errorf("decodificar categoria: %w", err))
	}
	c, err := it.categoria()
	if err != nil {
		return categoria.Categoria{}, errs.Internal(err)
	}
	return c, nil
}

// Visiveis implementa app.Store com uma Query por partição (SYSTEM e a do
// usuário), em leitura consistente para a categoria recém-criada aparecer na
// hora.
func (d *Dynamo) Visiveis(ctx context.Context, userID string) ([]categoria.Categoria, error) {
	donos := []string{categoria.DonoSistema}
	if userID != "" && userID != categoria.DonoSistema {
		donos = append(donos, userID)
	}
	var out []categoria.Categoria
	for _, dono := range donos {
		err := d.consultar(ctx, pk(dono), prefixoCat, false, func(raw map[string]types.AttributeValue) error {
			c, err := decodificar(raw)
			if err == nil {
				out = append(out, c)
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// consultar percorre todas as páginas de PK=pk e SK begins_with prefixo
// (prefixo vazio lê a partição inteira). soChaves projeta só PK e SK.
func (d *Dynamo) consultar(ctx context.Context, pk, prefixo string, soChaves bool, fn func(map[string]types.AttributeValue) error) error {
	in := &dynamodb.QueryInput{
		TableName:                 aws.String(d.table),
		KeyConditionExpression:    aws.String("PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": s(pk)},
		ConsistentRead:            aws.Bool(true),
	}
	if prefixo != "" {
		in.KeyConditionExpression = aws.String("PK = :pk AND begins_with(SK, :prefixo)")
		in.ExpressionAttributeValues[":prefixo"] = s(prefixo)
	}
	if soChaves {
		in.ProjectionExpression = aws.String("PK, SK")
	}
	for {
		out, err := d.api.Query(ctx, in)
		if err != nil {
			return errs.Internal(fmt.Errorf("consultar categorias: %w", err))
		}
		for _, raw := range out.Items {
			if err := fn(raw); err != nil {
				return err
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return nil
		}
		in.ExclusiveStartKey = out.LastEvaluatedKey
	}
}

// Obter implementa app.Store. O ID não diz o grupo, então lê as quatro
// chaves possíveis (padrão ou do usuário, em cada grupo) num BatchGetItem.
func (d *Dynamo) Obter(ctx context.Context, userID, id string) (categoria.Categoria, error) {
	donos := []string{categoria.DonoSistema}
	if userID != "" && userID != categoria.DonoSistema {
		donos = append(donos, userID)
	}
	var chaves []map[string]types.AttributeValue
	for _, dono := range donos {
		for _, g := range categoria.Grupos {
			chaves = append(chaves, map[string]types.AttributeValue{"PK": s(pk(dono)), "SK": s(skCat(g, id))})
		}
	}
	pedido := map[string]types.KeysAndAttributes{d.table: {Keys: chaves, ConsistentRead: aws.Bool(true)}}

	for tentativa := 1; len(pedido) > 0; tentativa++ {
		if tentativa > maxTentativas {
			return categoria.Categoria{}, errs.Internal(errors.New("ler categoria: chaves não processadas após as retentativas"))
		}
		if tentativa > 1 {
			d.espera(tentativa)
		}
		out, err := d.api.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{RequestItems: pedido})
		if err != nil {
			return categoria.Categoria{}, errs.Internal(fmt.Errorf("ler categoria: %w", err))
		}
		// Os IDs das padrão e das personalizadas nunca coincidem, então há no
		// máximo um item.
		if itens := out.Responses[d.table]; len(itens) > 0 {
			return decodificar(itens[0])
		}
		pedido = out.UnprocessedKeys
	}
	return categoria.Categoria{}, categoria.ErrNaoEncontrada()
}

// Criar implementa app.Store: grava a categoria e a trava de nome numa
// transação, as duas só se ainda não existirem.
func (d *Dynamo) Criar(ctx context.Context, c categoria.Categoria) error {
	if c.Padrao() {
		return errs.Internal(errors.New("criar: categoria padrão só é gravada pelo seed"))
	}
	avCat, err := attributevalue.MarshalMap(paraItem(c))
	if err != nil {
		return errs.Internal(fmt.Errorf("serializar categoria: %w", err))
	}
	avTrava, err := attributevalue.MarshalMap(trava{PK: pk(c.Dono), SK: skNome(c.Grupo, c.Nome), CategoriaID: c.ID})
	if err != nil {
		return errs.Internal(fmt.Errorf("serializar trava de nome: %w", err))
	}
	_, err = d.api.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{TableName: aws.String(d.table), Item: avCat, ConditionExpression: aws.String("attribute_not_exists(PK)")}},
			{Put: &types.Put{TableName: aws.String(d.table), Item: avTrava, ConditionExpression: aws.String("attribute_not_exists(PK)")}},
		},
	})
	if err == nil {
		return nil
	}
	if falhouCondicao(err, 1) {
		return categoria.ErrDuplicada(c.Grupo)
	}
	return errs.Internal(fmt.Errorf("gravar categoria: %w", err))
}

// Excluir implementa app.Store: apaga a categoria, se ainda existir, e a
// trava de nome dela, na mesma transação.
func (d *Dynamo) Excluir(ctx context.Context, c categoria.Categoria) error {
	if c.Padrao() {
		return errs.Internal(errors.New("excluir: categoria padrão não é apagada pelo store"))
	}
	_, err := d.api.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Delete: &types.Delete{
				TableName:           aws.String(d.table),
				Key:                 map[string]types.AttributeValue{"PK": s(pk(c.Dono)), "SK": s(skCat(c.Grupo, c.ID))},
				ConditionExpression: aws.String("attribute_exists(PK)"),
			}},
			{Delete: &types.Delete{
				TableName: aws.String(d.table),
				Key:       map[string]types.AttributeValue{"PK": s(pk(c.Dono)), "SK": s(skNome(c.Grupo, c.Nome))},
			}},
		},
	})
	if err == nil {
		return nil
	}
	if falhouCondicao(err, 0) {
		return categoria.ErrNaoEncontrada()
	}
	return errs.Internal(fmt.Errorf("excluir categoria: %w", err))
}

// falhouCondicao informa se a transação foi cancelada pela condição do item
// de índice i.
func falhouCondicao(err error, i int) bool {
	var tce *types.TransactionCanceledException
	if !errors.As(err, &tce) || i >= len(tce.CancellationReasons) {
		return false
	}
	return aws.ToString(tce.CancellationReasons[i].Code) == "ConditionalCheckFailed"
}

// ExcluirDoUsuario implementa app.Store: apaga toda a partição do usuário
// (categorias e travas de nome) em lotes e devolve quantas categorias havia.
func (d *Dynamo) ExcluirDoUsuario(ctx context.Context, userID string) (int, error) {
	if userID == "" || userID == categoria.DonoSistema {
		return 0, errs.Internal(errors.New("excluir do usuário: userId inválido"))
	}
	var pedidos []types.WriteRequest
	n := 0
	err := d.consultar(ctx, pk(userID), "", true, func(raw map[string]types.AttributeValue) error {
		if sk, ok := raw["SK"].(*types.AttributeValueMemberS); ok && strings.HasPrefix(sk.Value, prefixoCat) {
			n++
		}
		pedidos = append(pedidos, types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: map[string]types.AttributeValue{"PK": raw["PK"], "SK": raw["SK"]}}})
		return nil
	})
	if err != nil {
		return 0, err
	}
	if err := d.emLotes(ctx, pedidos); err != nil {
		return 0, err
	}
	return n, nil
}

// GravarPadrao implementa app.Store. Regrava os itens inteiros, então rodar
// de novo não duplica nada e corrige nome ou cor alterados.
func (d *Dynamo) GravarPadrao(ctx context.Context, cats []categoria.Categoria) error {
	pedidos := make([]types.WriteRequest, 0, len(cats))
	for _, c := range cats {
		if !c.Padrao() {
			return errs.Internal(fmt.Errorf("gravar padrão: %s não é categoria padrão", c.ID))
		}
		av, err := attributevalue.MarshalMap(paraItem(c))
		if err != nil {
			return errs.Internal(fmt.Errorf("serializar categoria padrão: %w", err))
		}
		pedidos = append(pedidos, types.WriteRequest{PutRequest: &types.PutRequest{Item: av}})
	}
	return d.emLotes(ctx, pedidos)
}

// emLotes envia os pedidos em lotes de 25, reenviando os não processados.
func (d *Dynamo) emLotes(ctx context.Context, pedidos []types.WriteRequest) error {
	for inicio := 0; inicio < len(pedidos); inicio += maxLote {
		lote := pedidos[inicio:min(inicio+maxLote, len(pedidos))]
		for tentativa := 1; len(lote) > 0; tentativa++ {
			if tentativa > maxTentativas {
				return errs.Internal(errors.New("gravar em lote: itens não processados após as retentativas"))
			}
			if tentativa > 1 {
				d.espera(tentativa)
			}
			out, err := d.api.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
				RequestItems: map[string][]types.WriteRequest{d.table: lote},
			})
			if err != nil {
				return errs.Internal(fmt.Errorf("gravar em lote: %w", err))
			}
			lote = out.UnprocessedItems[d.table]
		}
	}
	return nil
}

func s(v string) types.AttributeValue { return &types.AttributeValueMemberS{Value: v} }
