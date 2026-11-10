// Command local roda o categories-service na máquina do desenvolvedor,
// contra o DynamoDB Local do infra-base (task local:up).
//
// É um servidor HTTP que chama os mesmos handlers da Lambda:
//
//	POST /invoke  envelope do invoke (chamadas do BFF)
//	POST /events  eventos (UserDeleted)
//	GET  /healthz
//
// Ao subir, regrava as categorias padrão (idempotente), para a tabela local
// ficar igual à fonte da verdade mesmo sem o task local:seed.
//
// Escuta só em 127.0.0.1: o /invoke confia no userId do envelope, e na AWS
// quem garante isso é o IAM, que deixa só o BFF invocar a Lambda.
//
//	RUNTIME=local go run ./cmd/local
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/app"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/handler"
	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/store"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/config"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/events"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/invoke"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/logging"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

const servico = "categories-service"

// Config é a configuração do cmd/local.
type Config struct {
	Base           config.Base
	Port           string
	DynamoEndpoint string
	Table          string
	SemearPadrao   bool
}

// LoadConfig lê o ambiente. Só aceita RUNTIME=local e um DynamoDB local, para
// nunca gravar numa tabela da AWS por engano.
func LoadConfig(lookup config.LookupFunc) (Config, error) {
	base, err := config.LoadFrom(lookup, servico)
	if err != nil {
		return Config{}, err
	}
	if !base.IsLocal() {
		return Config{}, errors.New("cmd/local exige RUNTIME=local")
	}
	r := config.NewReader(lookup)
	semear, err := strconv.ParseBool(r.Get("SEED_PADRAO", "true"))
	if err != nil {
		return Config{}, fmt.Errorf("SEED_PADRAO inválido: %w", err)
	}
	c := Config{
		Base:           base,
		Port:           r.Get("PORT", "8083"),
		DynamoEndpoint: r.Get("DYNAMODB_ENDPOINT", "http://localhost:8000"),
		Table:          r.Get("CATEGORIES_TABLE", store.TableName),
		SemearPadrao:   semear,
	}
	if err := exigirLocal(c.DynamoEndpoint); err != nil {
		return Config{}, err
	}
	return c, nil
}

// exigirLocal aceita só a própria máquina ou um container do compose, como o
// seed do infra-base.
func exigirLocal(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("DYNAMODB_ENDPOINT inválido: %q", endpoint)
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1", "dynamodb", "host.docker.internal":
		return nil
	}
	return fmt.Errorf("DYNAMODB_ENDPOINT %q não é local; o cmd/local só roda contra o ambiente local", endpoint)
}

// NewHandler monta as rotas sobre os mesmos routers usados pela Lambda.
func NewHandler(svc *app.Service, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(invoke.Path, handler.Invoke(svc, log).HTTPHandler())
	mux.Handle(events.Path, handler.Events(svc, log).HTTPHandler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

func main() {
	if err := run(context.Background(), os.LookupEnv); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, lookup config.LookupFunc) error {
	cfg, err := LoadConfig(lookup)
	if err != nil {
		return err
	}
	log := logging.New(logging.Options{Service: servico, Env: cfg.Base.Env, Level: cfg.Base.LogLevel})

	db := dynamodb.New(dynamodb.Options{
		Region:       cfg.Base.Region,
		BaseEndpoint: aws.String(cfg.DynamoEndpoint),
		Credentials:  credentials.NewStaticCredentialsProvider("local", "local", ""),
	})
	svc := app.New(store.NewDynamo(db, cfg.Table), log)

	if cfg.SemearPadrao {
		seedCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := svc.SemearPadrao(seedCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("gravar categorias padrão em %s (rodou task local:up e local:seed?): %w", cfg.Table, err)
		}
	}

	srv := &http.Server{
		Addr:              net.JoinHostPort("127.0.0.1", cfg.Port),
		Handler:           NewHandler(svc, log),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	erro := make(chan error, 1)
	go func() { erro <- srv.ListenAndServe() }()
	log.Info("categories-service local no ar", "addr", "http://"+srv.Addr, "dynamodb", cfg.DynamoEndpoint, "table", cfg.Table)

	select {
	case err := <-erro:
		return err
	case <-ctx.Done():
		log.Info("encerrando")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
