package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/assertions"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/jsii-runtime-go"
)

const (
	busTeste = "gestor-gastos-dev"
	dlqTeste = "arn:aws:sqs:us-east-1:123456789012:gestor-gastos-dev-events-dlq"
)

var paramsTeste = Parametros{ParamBusName: busTeste, ParamDlqArn: dlqTeste}

// codigoTeste cria um bootstrap falso e o empacota como no synth de verdade.
func codigoTeste(t *testing.T) awslambda.Code {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "lambda")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bootstrap"), []byte("binario"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := CodigoLambda(dir)
	if c == nil {
		t.Fatal("CodigoLambda devolveu nil com o bootstrap presente")
	}
	return c
}

func novaStackTeste(t *testing.T, params Parametros) assertions.Template {
	t.Helper()
	app := awscdk.NewApp(nil)
	stack := NovoApp(app, "dev", codigoTeste(t), params)
	return assertions.Template_FromStack(stack, nil)
}

// idLogico devolve o id lógico do único recurso do tipo.
func idLogico(t *testing.T, tpl assertions.Template, tipo string) string {
	t.Helper()
	recursos := *tpl.FindResources(jsii.String(tipo), nil)
	if len(recursos) != 1 {
		t.Fatalf("esperava 1 %s, achei %d", tipo, len(recursos))
	}
	for id := range recursos {
		return id
	}
	return ""
}

func ref(id string) map[string]interface{} { return map[string]interface{}{"Ref": id} }

func TestNomeDaStack(t *testing.T) {
	app := awscdk.NewApp(nil)
	stack := NovoApp(app, "dev", codigoTeste(t), paramsTeste)
	if *stack.StackName() != "gestor-gastos-dev-categories" {
		t.Errorf("stack = %s", *stack.StackName())
	}
}

func TestTabelaOnDemandComRetain(t *testing.T) {
	tpl := novaStackTeste(t, paramsTeste)

	tpl.ResourceCountIs(jsii.String("AWS::DynamoDB::Table"), jsii.Number(1))
	tpl.HasResource(jsii.String("AWS::DynamoDB::Table"), map[string]interface{}{
		"DeletionPolicy":      "Retain",
		"UpdateReplacePolicy": "Retain",
		"Properties": map[string]interface{}{
			"TableName":   "gestor-gastos-dev-categories",
			"BillingMode": "PAY_PER_REQUEST",
			"KeySchema": []interface{}{
				map[string]interface{}{"AttributeName": "PK", "KeyType": "HASH"},
				map[string]interface{}{"AttributeName": "SK", "KeyType": "RANGE"},
			},
			"GlobalSecondaryIndexes": assertions.Match_Absent(),
		},
	})
}

func TestLambdaArm64De256MB(t *testing.T) {
	tpl := novaStackTeste(t, paramsTeste)
	tabela := idLogico(t, tpl, "AWS::DynamoDB::Table")

	// A outra função é a auxiliar do custom resource do seed (CDK).
	tpl.ResourceCountIs(jsii.String("AWS::Lambda::Function"), jsii.Number(2))
	tpl.HasResourceProperties(jsii.String("AWS::Lambda::Function"), map[string]interface{}{
		"FunctionName":  "gestor-gastos-dev-categories",
		"Runtime":       "provided.al2023",
		"Architectures": []interface{}{"arm64"},
		"Handler":       "bootstrap",
		"MemorySize":    256,
		"Timeout":       10,
		"Environment": map[string]interface{}{
			"Variables": map[string]interface{}{
				"RUNTIME":          "aws",
				"ENV":              "dev",
				"CATEGORIES_TABLE": ref(tabela),
				"LOG_LEVEL":        "info",
			},
		},
	})
}

func TestLogsPor7Dias(t *testing.T) {
	tpl := novaStackTeste(t, paramsTeste)

	tpl.HasResourceProperties(jsii.String("AWS::Logs::LogGroup"), map[string]interface{}{
		"LogGroupName":    "/aws/lambda/gestor-gastos-dev-categories",
		"RetentionInDays": 7,
	})
}

func TestAliasLiveComCanaryDoCodeDeploy(t *testing.T) {
	tpl := novaStackTeste(t, paramsTeste)
	app := idLogico(t, tpl, "AWS::CodeDeploy::Application")
	grupo := idLogico(t, tpl, "AWS::CodeDeploy::DeploymentGroup")
	alarme := idLogico(t, tpl, "AWS::CloudWatch::Alarm")

	tpl.HasResource(jsii.String("AWS::Lambda::Alias"), map[string]interface{}{
		"Properties": map[string]interface{}{"Name": "live"},
		"UpdatePolicy": map[string]interface{}{
			"CodeDeployLambdaAliasUpdate": map[string]interface{}{
				"ApplicationName":     ref(app),
				"DeploymentGroupName": ref(grupo),
			},
		},
	})

	tpl.HasResourceProperties(jsii.String("AWS::CodeDeploy::DeploymentGroup"), map[string]interface{}{
		"DeploymentGroupName":  "gestor-gastos-dev-categories",
		"DeploymentConfigName": "CodeDeployDefault.LambdaCanary10Percent5Minutes",
		"AutoRollbackConfiguration": map[string]interface{}{
			"Enabled": true,
			"Events": assertions.Match_ArrayWith(&[]interface{}{
				"DEPLOYMENT_FAILURE", "DEPLOYMENT_STOP_ON_ALARM",
			}),
		},
		"AlarmConfiguration": map[string]interface{}{
			"Enabled": true,
			"Alarms":  []interface{}{map[string]interface{}{"Name": ref(alarme)}},
		},
	})

	tpl.HasResourceProperties(jsii.String("AWS::CloudWatch::Alarm"), map[string]interface{}{
		"MetricName": "Errors",
		"Namespace":  "AWS/Lambda",
		"Threshold":  1,
		"Dimensions": assertions.Match_ArrayWith(&[]interface{}{
			map[string]interface{}{"Name": "Resource", "Value": assertions.Match_AnyValue()},
		}),
	})
}

func TestRegraUserDeletedNoAliasComDlq(t *testing.T) {
	tpl := novaStackTeste(t, paramsTeste)
	alias := idLogico(t, tpl, "AWS::Lambda::Alias")

	tpl.ResourceCountIs(jsii.String("AWS::Events::Rule"), jsii.Number(1))
	tpl.HasResourceProperties(jsii.String("AWS::Events::Rule"), map[string]interface{}{
		"Name":         "gestor-gastos-dev-categories-user-deleted",
		"EventBusName": busTeste,
		"EventPattern": assertions.Match_ObjectEquals(&map[string]interface{}{
			"source":      []interface{}{"gestor-gastos.users"},
			"detail-type": []interface{}{"UserDeleted"},
		}),
		"Targets": []interface{}{
			assertions.Match_ObjectLike(&map[string]interface{}{
				"Arn":              ref(alias),
				"DeadLetterConfig": map[string]interface{}{"Arn": dlqTeste},
				"RetryPolicy": map[string]interface{}{
					"MaximumRetryAttempts":     8,
					"MaximumEventAgeInSeconds": 7200,
				},
			}),
		},
	})

	// O EventBridge só pode invocar o alias, e só por esta regra.
	tpl.HasResourceProperties(jsii.String("AWS::Lambda::Permission"), map[string]interface{}{
		"Action":       "lambda:InvokeFunction",
		"FunctionName": ref(alias),
		"Principal":    "events.amazonaws.com",
		"SourceArn":    map[string]interface{}{"Fn::GetAtt": []interface{}{idLogico(t, tpl, "AWS::Events::Rule"), "Arn"}},
	})
}

func TestFalhaDaFuncaoNoEventoVaiParaADlq(t *testing.T) {
	tpl := novaStackTeste(t, paramsTeste)

	tpl.HasResourceProperties(jsii.String("AWS::Lambda::EventInvokeConfig"), map[string]interface{}{
		// Qualifier sai do ARN do alias (Fn::Select), ou seja, "live".
		"Qualifier":                assertions.Match_AnyValue(),
		"MaximumRetryAttempts":     2,
		"MaximumEventAgeInSeconds": 3600,
		"DestinationConfig": map[string]interface{}{
			"OnFailure": map[string]interface{}{"Destination": dlqTeste},
		},
	})
}

func TestPermissoesDaFuncao(t *testing.T) {
	tpl := novaStackTeste(t, paramsTeste)
	tabela := idLogico(t, tpl, "AWS::DynamoDB::Table")

	tpl.HasResourceProperties(jsii.String("AWS::IAM::Policy"), map[string]interface{}{
		"PolicyDocument": map[string]interface{}{
			"Statement": assertions.Match_ArrayWith(&[]interface{}{
				assertions.Match_ObjectLike(&map[string]interface{}{
					"Action":   assertions.Match_ArrayWith(&[]interface{}{"dynamodb:Query", "dynamodb:GetItem", "dynamodb:PutItem"}),
					"Effect":   "Allow",
					"Resource": assertions.Match_ArrayWith(&[]interface{}{map[string]interface{}{"Fn::GetAtt": []interface{}{tabela, "Arn"}}}),
				}),
				assertions.Match_ObjectLike(&map[string]interface{}{
					"Action":   assertions.Match_ArrayWith(&[]interface{}{"sqs:SendMessage"}),
					"Effect":   "Allow",
					"Resource": dlqTeste,
				}),
			}),
		},
	})

	// O serviço não fala com o Cognito nem publica eventos.
	politicas, err := json.Marshal(tpl.FindResources(jsii.String("AWS::IAM::Policy"), nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, acao := range []string{"cognito-idp:", "events:PutEvents"} {
		if strings.Contains(string(politicas), acao) {
			t.Errorf("a função não deveria ter %s", acao)
		}
	}
}

func TestPublicaArnDoAliasENomeDaTabelaNoSsm(t *testing.T) {
	tpl := novaStackTeste(t, paramsTeste)
	alias := idLogico(t, tpl, "AWS::Lambda::Alias")
	tabela := idLogico(t, tpl, "AWS::DynamoDB::Table")

	tpl.ResourceCountIs(jsii.String("AWS::SSM::Parameter"), jsii.Number(2))
	tpl.HasResourceProperties(jsii.String("AWS::SSM::Parameter"), map[string]interface{}{
		"Name":  "/gestor-gastos/dev/categories/function-arn",
		"Type":  "String",
		"Tier":  "Standard",
		"Value": ref(alias),
	})
	tpl.HasResourceProperties(jsii.String("AWS::SSM::Parameter"), map[string]interface{}{
		"Name":  "/gestor-gastos/dev/categories/table-name",
		"Value": ref(tabela),
	})
}

func TestSemExports(t *testing.T) {
	tpl := novaStackTeste(t, paramsTeste)
	saidas := *tpl.FindOutputs(jsii.String("*"), nil)
	for nome, s := range saidas {
		if _, ok := (*s)["Export"]; ok {
			t.Errorf("a saída %s não deveria ter export", nome)
		}
	}
}

func TestValoresLidosNoSynthNaoViramParametrosDoCloudFormation(t *testing.T) {
	tpl := novaStackTeste(t, paramsTeste)
	for nome := range *tpl.FindParameters(jsii.String("*"), nil) {
		if nome != "BootstrapVersion" {
			t.Errorf("parâmetro inesperado no template: %s", nome)
		}
	}
}

func TestSemLeituraNoSynthUsaParametrosDoCloudFormation(t *testing.T) {
	tpl := novaStackTeste(t, nil)

	for _, p := range ParametrosLidos {
		tpl.HasParameter(jsii.String("*"), map[string]interface{}{
			"Type":    "AWS::SSM::Parameter::Value<String>",
			"Default": CaminhoSSM("dev", p),
		})
	}
	tpl.HasResourceProperties(jsii.String("AWS::Events::Rule"), map[string]interface{}{
		"EventBusName": map[string]interface{}{"Ref": assertions.Match_StringLikeRegexp(jsii.String("SsmParameterValue.*"))},
	})
}

func TestSemBinarioAStackFalhaComMensagemClara(t *testing.T) {
	if CodigoLambda(filepath.Join(t.TempDir(), "nao-existe")) != nil {
		t.Fatal("esperava nil sem o bootstrap")
	}

	app := awscdk.NewApp(nil)
	stack := NovoApp(app, "dev", nil, paramsTeste)
	assertions.Annotations_FromStack(stack).HasError(jsii.String("*"), jsii.String(MsgFaltaBuild))
}

func TestSeedDasPadraoAcadaVersaoNova(t *testing.T) {
	tpl := novaStackTeste(t, paramsTeste)
	versao := idLogico(t, tpl, "AWS::Lambda::Version")

	// Custom::AWS invoca a versão nova da função com {"tarefa":"SemearPadrao"}.
	recursos := *tpl.FindResources(jsii.String("Custom::AWS"), nil)
	if len(recursos) != 1 {
		t.Fatalf("esperava 1 Custom::AWS, achei %d", len(recursos))
	}
	b, err := json.Marshal(recursos)
	if err != nil {
		t.Fatal(err)
	}
	cr := string(b)
	for _, trecho := range []string{`\"service\":\"Lambda\"`, `\"action\":\"invoke\"`, `SemearPadrao`, versao} {
		if !strings.Contains(cr, trecho) {
			t.Errorf("custom resource sem %s: %s", trecho, cr)
		}
	}

	// A auxiliar do CDK só pode invocar a versão da função.
	tpl.HasResourceProperties(jsii.String("AWS::IAM::Policy"), map[string]interface{}{
		"PolicyDocument": map[string]interface{}{
			"Statement": []interface{}{
				map[string]interface{}{
					"Action":   "lambda:InvokeFunction",
					"Effect":   "Allow",
					"Resource": ref(versao),
				},
			},
		},
	})
}
