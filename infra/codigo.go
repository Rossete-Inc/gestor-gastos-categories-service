package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/jsii-runtime-go"
)

const (
	// DirLambda é onde fica o binário da função, a partir de infra/. Segue a
	// convenção do go-service.yml: <dist-dir>/<pasta>/bootstrap, compilado de
	// ./cmd/lambda.
	DirLambda = "../dist/lambda"

	// MsgFaltaBuild é o erro de synth quando o binário não existe.
	MsgFaltaBuild = "stack categories: o binário da Lambda não existe em " + DirLambda + "/bootstrap. " +
		"Compile antes, pela raiz do repositório: GOOS=linux GOARCH=arm64 CGO_ENABLED=0 " +
		"go build -tags lambda.norpc -trimpath -ldflags=\"-s -w\" -o dist/lambda/bootstrap ./cmd/lambda"
)

// CodigoLambda empacota <dir>/bootstrap em <dir>.zip e devolve esse zip como
// asset, ou nil se o bootstrap não existir.
//
// Mesma solução do infra-base: o zip é montado aqui, e não pelo CLI do CDK,
// porque no Windows o CLI geraria o zip sem o bit de execução e a Lambda
// falharia com "permission denied". O modo é sempre 0755 e a data é fixa,
// para o hash do asset (e a versão da Lambda) só mudar quando o binário muda.
func CodigoLambda(dir string) awslambda.Code {
	destino, err := zipBootstrap(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		panic(err)
	}
	return awslambda.Code_FromAsset(jsii.String(destino), nil)
}

func zipBootstrap(dir string) (string, error) {
	bin, err := os.ReadFile(filepath.Join(dir, "bootstrap"))
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	cab := &zip.FileHeader{Name: "bootstrap", Method: zip.Deflate, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
	cab.SetMode(0o755)
	w, err := zw.CreateHeader(cab)
	if err == nil {
		_, err = w.Write(bin)
	}
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		return "", fmt.Errorf("empacotar %s: %w", dir, err)
	}

	destino := filepath.Clean(dir) + ".zip"
	if err := os.WriteFile(destino, buf.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("gravar %s: %w", destino, err)
	}
	return destino, nil
}
