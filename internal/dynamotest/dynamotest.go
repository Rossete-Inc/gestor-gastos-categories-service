// Package dynamotest cria tabelas temporárias no DynamoDB Local para os
// testes de integração. Só é importado por testes.
package dynamotest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Endpoint devolve DYNAMODB_ENDPOINT ou o padrão do ambiente local.
func Endpoint() string {
	if e := os.Getenv("DYNAMODB_ENDPOINT"); e != "" {
		return e
	}
	return "http://localhost:8000"
}

// Client cria um client para o endpoint local, com credenciais fixas.
func Client(endpoint string) *dynamodb.Client {
	return dynamodb.New(dynamodb.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider("local", "local", ""),
	})
}

// CategoriesTable cria uma tabela com o esquema de gestor-gastos-categories
// (o mesmo do seed do infra-base: PK e SK string, sem índices) e um nome
// único, e a apaga no fim do teste.
func CategoriesTable(t *testing.T, prefixo string) (*dynamodb.Client, string) {
	t.Helper()
	c := Client(Endpoint())
	var b [4]byte
	_, _ = rand.Read(b[:])
	nome := prefixo + "-test-" + hex.EncodeToString(b[:])

	_, err := c.CreateTable(context.Background(), &dynamodb.CreateTableInput{
		TableName:   aws.String(nome),
		BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("PK"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("SK"), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("PK"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("SK"), KeyType: types.KeyTypeRange},
		},
	})
	if err != nil {
		t.Fatalf("criar tabela no DynamoDB Local (%s); o ambiente está no ar (task local:up)? %v", Endpoint(), err)
	}
	t.Cleanup(func() {
		_, _ = c.DeleteTable(context.Background(), &dynamodb.DeleteTableInput{TableName: aws.String(nome)})
	})
	return c, nome
}
