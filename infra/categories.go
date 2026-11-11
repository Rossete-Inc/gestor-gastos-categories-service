package main

import (
	"encoding/json"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscloudwatch"
	"github.com/aws/aws-cdk-go/awscdk/v2/awscodedeploy"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsdynamodb"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsevents"
	"github.com/aws/aws-cdk-go/awscdk/v2/awseventstargets"
	"github.com/aws/aws-cdk-go/awscdk/v2/awsiam"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambdadestinations"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslogs"
	"github.com/aws/aws-cdk-go/awscdk/v2/awssqs"
	"github.com/aws/aws-cdk-go/awscdk/v2/customresources"
	"github.com/aws/aws-cdk-go/awscdk/v2/interfaces/interfacesawscloudwatch"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

// CategoriesProps configura a stack categories.
type CategoriesProps struct {
	awscdk.StackProps
	Ambiente string
	// Codigo é o pacote da Lambda. nil = binário ausente: a stack registra
	// MsgFaltaBuild e não é sintetizada.
	Codigo awslambda.Code
	// Parametros são os valores lidos do SSM no synth (LerParametrosSSM).
	// nil = parâmetros do CloudFormation resolvidos no deploy.
	Parametros Parametros
}

// NovaStackCategories cria a stack gestor-gastos-<env>-categories:
//
//   - tabela DynamoDB on-demand com RETAIN (PK/SK, como internal/store);
//   - Lambda arm64 de 256 MB com alias live, que atende o invoke do BFF e o
//     UserDeleted;
//   - CodeDeploy com canary de 10% por 5 minutos e rollback automático por
//     falha ou alarme de erros;
//   - regra do EventBridge para UserDeleted, com retentativas e DLQ;
//   - seed das categorias padrão a cada versão nova da função;
//   - logs por 7 dias;
//   - ARN do alias e nome da tabela publicados no SSM para o BFF.
func NovaStackCategories(scope constructs.Construct, id string, props *CategoriesProps) awscdk.Stack {
	stack := awscdk.NewStack(scope, jsii.String(id), &props.StackProps)
	env := props.Ambiente

	if props.Codigo == nil {
		awscdk.Annotations_Of(stack).AddError(jsii.String(MsgFaltaBuild))
		return stack
	}

	params := props.Parametros
	if params == nil {
		params = ParametrosCloudFormation(stack, env)
	}

	nome := NomeStack(env, NomeServico)

	tabela := novaTabela(stack, nome)
	fn, alias := novaFuncao(stack, env, nome, props.Codigo, tabela)

	dlq := awssqs.Queue_FromQueueArn(stack, jsii.String("Dlq"), jsii.String(params[ParamDlqArn]))

	// Erro da função numa invocação assíncrona (o EventBridge entrega assim):
	// a Lambda tenta mais 2 vezes e depois manda o registro para a DLQ do
	// barramento. A DLQ do target da regra (abaixo) só cobre falhas de entrega.
	alias.ConfigureAsyncInvoke(&awslambda.EventInvokeConfigOptions{
		OnFailure:     awslambdadestinations.NewSqsDestination(dlq),
		RetryAttempts: jsii.Number(2),
		MaxEventAge:   awscdk.Duration_Hours(jsii.Number(1)),
	})

	novoCanary(stack, nome, alias)

	barramento := awsevents.EventBus_FromEventBusName(stack, jsii.String("Barramento"), jsii.String(params[ParamBusName]))
	awsevents.NewRule(stack, jsii.String("UserDeleted"), &awsevents.RuleProps{
		RuleName:    jsii.String(nome + "-user-deleted"),
		Description: jsii.String("UserDeleted do users-service: apaga as categorias personalizadas do usuário"),
		EventBus:    barramento,
		EventPattern: &awsevents.EventPattern{
			Source:     jsii.Strings(SourceUsers),
			DetailType: jsii.Strings(DetailTypeUserDeleted),
		},
		Targets: &[]awsevents.IRuleTarget{
			awseventstargets.NewLambdaFunction(alias, &awseventstargets.LambdaFunctionProps{
				DeadLetterQueue: dlq,
				RetryAttempts:   jsii.Number(8),
				MaxEventAge:     awscdk.Duration_Hours(jsii.Number(2)),
			}),
		},
	})

	novoSeedPadrao(stack, fn)

	publicarParametro(stack, env, ParamFunctionArn, *alias.FunctionArn(),
		"ARN do alias live da Lambda do categories-service, invocado pelo BFF")
	publicarParametro(stack, env, ParamTableName, *tabela.TableName(),
		"Nome da tabela DynamoDB do categories-service")

	// Saída informativa (sem exportName: quem consome lê do SSM).
	awscdk.NewCfnOutput(stack, jsii.String("FunctionArn"), &awscdk.CfnOutputProps{
		Value:       alias.FunctionArn(),
		Description: jsii.String("ARN do alias live (também em " + CaminhoSSM(env, ParamFunctionArn) + ")"),
	})

	return stack
}

// novaTabela cria a tabela no formato de internal/store: PK=SYSTEM|USER#<id>,
// SK=CAT#<grupo>#<id> ou NOME#<grupo>#<nome>. Sem índices secundários.
// On-demand: sem custo parada além do armazenamento. RETAIN: apagar a stack
// não apaga as categorias.
func novaTabela(stack awscdk.Stack, nome string) awsdynamodb.Table {
	texto := func(n string) *awsdynamodb.Attribute {
		return &awsdynamodb.Attribute{Name: jsii.String(n), Type: awsdynamodb.AttributeType_STRING}
	}
	return awsdynamodb.NewTable(stack, jsii.String("Tabela"), &awsdynamodb.TableProps{
		TableName:     jsii.String(nome),
		PartitionKey:  texto("PK"),
		SortKey:       texto("SK"),
		BillingMode:   awsdynamodb.BillingMode_PAY_PER_REQUEST,
		RemovalPolicy: awscdk.RemovalPolicy_RETAIN,
	})
}

// novaFuncao cria a Lambda e o alias live, que aponta sempre para a versão
// atual. Quem move o tráfego do alias entre versões é o CodeDeploy.
func novaFuncao(stack awscdk.Stack, env, nome string, codigo awslambda.Code, tabela awsdynamodb.Table) (awslambda.Function, awslambda.Alias) {
	logs := awslogs.NewLogGroup(stack, jsii.String("Logs"), &awslogs.LogGroupProps{
		LogGroupName:  jsii.String("/aws/lambda/" + nome),
		Retention:     awslogs.RetentionDays_ONE_WEEK,
		RemovalPolicy: awscdk.RemovalPolicy_DESTROY,
	})

	fn := awslambda.NewFunction(stack, jsii.String("Funcao"), &awslambda.FunctionProps{
		FunctionName: jsii.String(nome),
		Description:  jsii.String("categories-service: invoke do BFF, consumidor de UserDeleted e seed das padrão"),
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Handler:      jsii.String("bootstrap"),
		Code:         codigo,
		MemorySize:   jsii.Number(256),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(10)),
		LogGroup:     logs,
		Environment: &map[string]*string{
			"RUNTIME":          jsii.String("aws"),
			"ENV":              jsii.String(env),
			"CATEGORIES_TABLE": tabela.TableName(),
			"LOG_LEVEL":        jsii.String("info"),
		},
	})
	tabela.GrantReadWriteData(fn)

	alias := awslambda.NewAlias(stack, jsii.String("Live"), &awslambda.AliasProps{
		AliasName:   jsii.String(AliasLive),
		Version:     fn.CurrentVersion(),
		Description: jsii.String("Versão em produção; o CodeDeploy faz o canary entre versões"),
	})
	return fn, alias
}

// novoSeedPadrao grava as categorias padrão depois do deploy, invocando a
// versão nova da função com {"tarefa":"SemearPadrao"} (handler.Lambda).
//
// A fonte da verdade das padrão é o código (internal/categoria/padrao.go),
// então o seed roda sempre que o código muda: o physical id é o número da
// versão, que muda junto com o binário. O seed é idempotente.
//
// Custo: o custom resource cria uma Lambda auxiliar do CDK, que só roda no
// deploy.
func novoSeedPadrao(stack awscdk.Stack, fn awslambda.Function) customresources.AwsCustomResource {
	versao := fn.CurrentVersion()
	payload, _ := json.Marshal(map[string]string{"tarefa": TarefaSemearPadrao})
	chamada := &customresources.AwsSdkCall{
		Service: jsii.String("Lambda"),
		Action:  jsii.String("invoke"),
		Parameters: map[string]interface{}{
			"FunctionName":   versao.FunctionArn(),
			"InvocationType": "RequestResponse",
			"Payload":        string(payload),
		},
		PhysicalResourceId: customresources.PhysicalResourceId_Of(versao.Version()),
	}
	return customresources.NewAwsCustomResource(stack, jsii.String("SeedPadrao"), &customresources.AwsCustomResourceProps{
		OnCreate:            chamada,
		OnUpdate:            chamada,
		InstallLatestAwsSdk: jsii.Bool(false),
		Policy: customresources.AwsCustomResourcePolicy_FromStatements(&[]awsiam.PolicyStatement{
			awsiam.NewPolicyStatement(&awsiam.PolicyStatementProps{
				Actions:   jsii.Strings("lambda:InvokeFunction"),
				Resources: jsii.Strings(*versao.FunctionArn()),
			}),
		}),
	})
}

// novoCanary liga o alias ao CodeDeploy: cada versão nova recebe 10% do
// tráfego por 5 minutos e só depois os 100%. Volta sozinho para a versão
// anterior se o deploy falhar ou se o alarme de erros disparar.
//
// O alarme não tem ação (sem SNS) e fica dentro dos 10 alarmes gratuitos.
func novoCanary(stack awscdk.Stack, nome string, alias awslambda.Alias) awscodedeploy.LambdaDeploymentGroup {
	erros := awscloudwatch.NewAlarm(stack, jsii.String("ErrosNoAlias"), &awscloudwatch.AlarmProps{
		AlarmName:          jsii.String(nome + "-erros"),
		AlarmDescription:   jsii.String("Erros no alias live do categories-service; durante o canary, dispara o rollback"),
		Metric:             alias.MetricErrors(&awscloudwatch.MetricOptions{Period: awscdk.Duration_Minutes(jsii.Number(1))}),
		Threshold:          jsii.Number(1),
		EvaluationPeriods:  jsii.Number(1),
		ComparisonOperator: awscloudwatch.ComparisonOperator_GREATER_THAN_OR_EQUAL_TO_THRESHOLD,
		TreatMissingData:   awscloudwatch.TreatMissingData_NOT_BREACHING,
	})

	return awscodedeploy.NewLambdaDeploymentGroup(stack, jsii.String("Deploy"), &awscodedeploy.LambdaDeploymentGroupProps{
		DeploymentGroupName: jsii.String(nome),
		Alias:               alias,
		DeploymentConfig:    awscodedeploy.LambdaDeploymentConfig_CANARY_10PERCENT_5MINUTES(),
		Alarms:              &[]interfacesawscloudwatch.IAlarmRef{erros},
		AutoRollback: &awscodedeploy.AutoRollbackConfig{
			FailedDeployment:  jsii.Bool(true),
			StoppedDeployment: jsii.Bool(true),
			DeploymentInAlarm: jsii.Bool(true),
		},
	})
}
