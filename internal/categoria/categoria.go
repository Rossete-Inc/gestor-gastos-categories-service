// Package categoria é o domínio do categories-service: grupos de gasto,
// categorias padrão do sistema e categorias personalizadas de cada usuário
// (spec categorias).
package categoria

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// Grupo de gasto. Toda categoria pertence a exatamente um.
type Grupo string

const (
	// Cartao reúne os gastos feitos no cartão.
	Cartao Grupo = contract.GrupoCartao
	// Fixo reúne os gastos fixos recorrentes.
	Fixo Grupo = contract.GrupoFixo
)

// Grupos lista os grupos válidos, na ordem em que aparecem para o usuário.
var Grupos = []Grupo{Cartao, Fixo}

// Valido informa se o grupo existe.
func (g Grupo) Valido() bool { return slices.Contains(Grupos, g) }

// DonoSistema é o dono das categorias padrão, visíveis a todos os usuários.
const DonoSistema = "SYSTEM"

// MaxNome é o tamanho máximo do nome, em caracteres (openapi: CategoriaIn).
const MaxNome = 40

var padraoCor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// Categoria padrão (Dono = DonoSistema) ou personalizada (Dono = userId).
type Categoria struct {
	ID       string
	Dono     string
	Grupo    Grupo
	Nome     string
	Cor      string
	CriadoEm time.Time // zero nas categorias padrão
}

// Padrao informa se a categoria é do sistema.
func (c Categoria) Padrao() bool { return c.Dono == DonoSistema }

// VisivelPara informa se o usuário enxerga a categoria: as padrão são de
// todos, as personalizadas só do próprio dono.
func (c Categoria) VisivelPara(userID string) bool {
	return c.Padrao() || (userID != "" && c.Dono == userID)
}

// ChaveNome é a forma do nome usada para comparar duplicidade.
func (c Categoria) ChaveNome() string { return ChaveNome(c.Nome) }

// NormalizarNome deixa o nome em NFC, sem espaços nas pontas e sem espaços
// repetidos. NFC faz "Água" digitado com acento combinado (A + ´) ser igual
// ao "Água" com o caractere pronto.
func NormalizarNome(nome string) string {
	return strings.Join(strings.Fields(norm.NFC.String(nome)), " ")
}

// ChaveNome é o nome normalizado em minúsculas. A spec manda ignorar
// maiúsculas e minúsculas; acentos continuam contando ("Agua" e "Água" são
// nomes diferentes).
func ChaveNome(nome string) string { return strings.ToLower(NormalizarNome(nome)) }

// Nova valida e cria uma categoria personalizada do usuário. O nome é
// normalizado (NormalizarNome) e a cor gravada em maiúsculas, como as das
// categorias padrão.
func Nova(id, userID, nome string, grupo Grupo, cor string, agora time.Time) (Categoria, error) {
	if strings.TrimSpace(userID) == "" || userID == DonoSistema {
		return Categoria{}, errs.BadRequest(contract.CodigoUsuarioAusente, "Requisição sem usuário.")
	}
	nome = NormalizarNome(nome)
	cor = strings.ToUpper(strings.TrimSpace(cor))

	var campos []errs.FieldError
	if msg := validarNome(nome); msg != "" {
		campos = append(campos, errs.FieldError{Field: "nome", Message: msg})
	}
	if !grupo.Valido() {
		campos = append(campos, errs.FieldError{Field: "grupo", Message: "deve ser CARTAO ou FIXO"})
	}
	if !padraoCor.MatchString(cor) {
		campos = append(campos, errs.FieldError{Field: "cor", Message: "deve estar no formato #RRGGBB"})
	}
	if len(campos) > 0 {
		return Categoria{}, errs.Validation("Dados da categoria inválidos.", campos...)
	}
	return Categoria{
		ID: id, Dono: userID, Grupo: grupo, Nome: nome, Cor: cor,
		CriadoEm: agora.UTC().Truncate(time.Millisecond),
	}, nil
}

func validarNome(nome string) string {
	if nome == "" {
		return "obrigatório"
	}
	if utf8.RuneCountInString(nome) > MaxNome {
		return "deve ter no máximo 40 caracteres"
	}
	for _, r := range nome {
		if unicode.IsControl(r) {
			return "contém caracteres inválidos"
		}
	}
	return ""
}

// ExigirNomeDisponivel recusa a nova categoria se já houver, entre as
// visíveis ao usuário, outra no mesmo grupo com o mesmo nome ignorando
// maiúsculas e minúsculas (cenário "Nome duplicado no grupo"). O mesmo nome
// em outro grupo é permitido.
func ExigirNomeDisponivel(visiveis []Categoria, nova Categoria) error {
	chave := nova.ChaveNome()
	for _, c := range visiveis {
		if c.Grupo == nova.Grupo && c.VisivelPara(nova.Dono) && c.ChaveNome() == chave {
			return ErrDuplicada(nova.Grupo)
		}
	}
	return nil
}

// ErrDuplicada é o 409 de nome repetido no grupo.
func ErrDuplicada(g Grupo) error {
	return errs.Conflict(contract.CodigoDuplicada, "Já existe uma categoria com este nome no grupo "+string(g)+".")
}

// ErrNaoEncontrada é o 404 de categoria inexistente ou de outro usuário. Os
// dois casos têm a mesma resposta, para não revelar categorias alheias.
func ErrNaoEncontrada() error {
	return errs.NotFound(contract.CodigoNaoEncontrada, "Categoria não encontrada.")
}

// ExigirExcluivel autoriza o usuário a excluir (ou alterar) a categoria:
// padrão é imutável (403 "categoria_padrao"), e a de outro usuário é tratada
// como inexistente (404).
func (c Categoria) ExigirExcluivel(userID string) error {
	if c.Padrao() {
		return errs.Forbidden(contract.CodigoPadrao, "Categorias padrão não podem ser alteradas nem excluídas.")
	}
	if !c.VisivelPara(userID) {
		return ErrNaoEncontrada()
	}
	return nil
}

// Visiveis filtra as categorias que o usuário enxerga.
func Visiveis(cats []Categoria, userID string) []Categoria {
	var out []Categoria
	for _, c := range cats {
		if c.VisivelPara(userID) {
			out = append(out, c)
		}
	}
	return out
}

// Ordenar põe as categorias em ordem alfabética do português, sem diferenciar
// maiúsculas e minúsculas e com "Água" junto do "A". Empate fica pelo ID,
// para a ordem ser estável.
func Ordenar(cats []Categoria) {
	col := collate.New(language.BrazilianPortuguese, collate.IgnoreCase)
	slices.SortStableFunc(cats, func(a, b Categoria) int {
		if r := col.CompareString(a.Nome, b.Nome); r != 0 {
			return r
		}
		return strings.Compare(a.ID, b.ID)
	})
}

// PorGrupo separa as categorias por grupo, cada grupo em ordem alfabética.
// Todos os grupos aparecem no mapa, mesmo vazios.
func PorGrupo(cats []Categoria) map[Grupo][]Categoria {
	out := make(map[Grupo][]Categoria, len(Grupos))
	for _, g := range Grupos {
		out[g] = []Categoria{}
	}
	for _, c := range cats {
		if c.Grupo.Valido() {
			out[c.Grupo] = append(out[c.Grupo], c)
		}
	}
	for _, g := range Grupos {
		Ordenar(out[g])
	}
	return out
}

// Contract converte para o formato do contrato público.
func (c Categoria) Contract() contract.Categoria {
	return contract.Categoria{ID: c.ID, Nome: c.Nome, Grupo: string(c.Grupo), Cor: c.Cor, Padrao: c.Padrao()}
}
