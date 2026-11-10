package categoria

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/contract"
	"github.com/Rossete-Inc/gestor-gastos-go-commons/errs"
)

var agora = time.Date(2026, 11, 10, 12, 0, 0, 123456789, time.UTC)

func nova(t *testing.T, id, userID, nome string, g Grupo) Categoria {
	t.Helper()
	c, err := Nova(id, userID, nome, g, "#112233", agora)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Requirement "Grupos de gasto", cenário "Categoria sem grupo".
func TestCategoriaSemGrupo(t *testing.T) {
	for _, g := range []Grupo{"", "cartao", "OUTRO", "FIXO "} {
		_, err := Nova("c1", "u-1", "Pet", g, "#112233", agora)
		e := errs.As(err)
		if e == nil || e.Kind != errs.KindValidation || len(e.Fields) != 1 || e.Fields[0].Field != "grupo" {
			t.Errorf("grupo %q: erro = %+v", g, e)
		}
	}
	for _, g := range Grupos {
		if _, err := Nova("c1", "u-1", "Pet", g, "#112233", agora); err != nil {
			t.Errorf("grupo %s: %v", g, err)
		}
	}
}

func TestNovaValidaCampos(t *testing.T) {
	casos := map[string]struct {
		nome, cor string
		campos    []string
	}{
		"nome vazio":         {"   ", "#112233", []string{"nome"}},
		"nome longo":         {strings.Repeat("á", MaxNome+1), "#112233", []string{"nome"}},
		"nome com controle":  {"Pet\x00", "#112233", []string{"nome"}},
		"cor sem #":          {"Pet", "112233", []string{"cor"}},
		"cor curta":          {"Pet", "#123", []string{"cor"}},
		"cor não hex":        {"Pet", "#GGGGGG", []string{"cor"}},
		"tudo errado":        {"", "azul", []string{"nome", "cor"}},
		"nome no limite":     {strings.Repeat("á", MaxNome), "#112233", nil},
		"cor em minúsculas":  {"Pet", "#abcdef", nil},
		"cor com espaços":    {"Pet", " #abcdef ", nil},
		"nome com espaços":   {"  Pet   de   casa ", "#112233", nil},
		"nome só emoji ok":   {"🐶", "#112233", nil},
		"nome com acento ok": {"Farmácia", "#112233", nil},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			_, err := Nova("c1", "u-1", c.nome, Cartao, c.cor, agora)
			if c.campos == nil {
				if err != nil {
					t.Fatalf("erro inesperado: %v", err)
				}
				return
			}
			e := errs.As(err)
			if e == nil || e.Kind != errs.KindValidation {
				t.Fatalf("erro = %v", err)
			}
			var got []string
			for _, f := range e.Fields {
				got = append(got, f.Field)
			}
			if !slices.Equal(got, c.campos) {
				t.Errorf("campos = %v, quer %v", got, c.campos)
			}
		})
	}
}

func TestNovaNormaliza(t *testing.T) {
	c, err := Nova("c1", "u-1", "  Pet   de  casa ", Fixo, "#abcdef", agora)
	if err != nil {
		t.Fatal(err)
	}
	quer := Categoria{ID: "c1", Dono: "u-1", Grupo: Fixo, Nome: "Pet de casa", Cor: "#ABCDEF", CriadoEm: agora.Truncate(time.Millisecond)}
	if c != quer {
		t.Errorf("c = %+v\nquer %+v", c, quer)
	}
	if c.Padrao() {
		t.Error("personalizada não pode ser padrão")
	}
}

func TestNovaSemUsuario(t *testing.T) {
	// "SYSTEM" é o dono das padrão: ninguém cria categoria em nome dele.
	for _, u := range []string{"", "  ", DonoSistema} {
		_, err := Nova("c1", u, "Pet", Cartao, "#112233", agora)
		if !errors.Is(err, errs.BadRequest(contract.CodigoUsuarioAusente, "")) {
			t.Errorf("usuário %q: erro = %v", u, err)
		}
	}
}

// Requirement "Categorias padrão": as 12 da spec, 6 em cada grupo.
func TestCategoriasPadrao(t *testing.T) {
	quer := map[Grupo][]string{
		Cartao: {"Alimentação", "Compras", "Lazer", "Outros", "Saúde", "Transporte"},
		Fixo:   {"Água", "Aluguel", "Condomínio", "Internet", "Luz", "Telefone"},
	}
	pg := PorGrupo(Padrao())
	for _, g := range Grupos {
		var nomes []string
		for _, c := range pg[g] {
			nomes = append(nomes, c.Nome)
			if !c.Padrao() || !padraoCor.MatchString(c.Cor) || c.Cor != strings.ToUpper(c.Cor) || c.ID == "" {
				t.Errorf("categoria padrão malformada: %+v", c)
			}
		}
		if !slices.Equal(nomes, quer[g]) {
			t.Errorf("%s = %v, quer %v", g, nomes, quer[g])
		}
	}
	// IDs únicos.
	ids := map[string]bool{}
	for _, c := range Padrao() {
		if ids[c.ID] {
			t.Errorf("ID repetido: %s", c.ID)
		}
		ids[c.ID] = true
	}
	// Padrao devolve cópia: alterar o resultado não muda a lista.
	p := Padrao()
	p[0].Nome = "Mudou"
	if Padrao()[0].Nome == "Mudou" {
		t.Error("Padrao não devolve cópia")
	}
}

// Cenário "Usuário novo": vê todas as padrão, agrupadas.
func TestUsuarioNovoVeTodasAsPadrao(t *testing.T) {
	v := Visiveis(Padrao(), "novo")
	if len(v) != 12 {
		t.Fatalf("visíveis = %d", len(v))
	}
	pg := PorGrupo(v)
	if len(pg[Cartao]) != 6 || len(pg[Fixo]) != 6 {
		t.Errorf("por grupo = %d/%d", len(pg[Cartao]), len(pg[Fixo]))
	}
}

// Cenário "Categoria padrão é imutável para o usuário".
func TestPadraoImutavel(t *testing.T) {
	for _, c := range Padrao() {
		err := c.ExigirExcluivel("u-1")
		if !errors.Is(err, errs.Forbidden(contract.CodigoPadrao, "")) {
			t.Errorf("%s: erro = %v", c.ID, err)
		}
	}
}

func TestExcluivelSoPeloDono(t *testing.T) {
	c := nova(t, "c1", "u-1", "Pet", Cartao)
	if err := c.ExigirExcluivel("u-1"); err != nil {
		t.Errorf("dono: %v", err)
	}
	for _, outro := range []string{"u-2", ""} {
		if err := c.ExigirExcluivel(outro); !errors.Is(err, errs.NotFound(contract.CodigoNaoEncontrada, "")) {
			t.Errorf("usuário %q: erro = %v", outro, err)
		}
	}
}

// Cenários "Criar categoria" e "Isolamento entre usuários".
func TestVisibilidade(t *testing.T) {
	petA := nova(t, "c1", "u-a", "Pet", Cartao)
	academiaB := nova(t, "c2", "u-b", "Academia", Cartao)
	todas := append(Padrao(), petA, academiaB)

	paraA := PorGrupo(Visiveis(todas, "u-a"))
	if !slices.ContainsFunc(paraA[Cartao], func(c Categoria) bool { return c.ID == "c1" }) {
		t.Error("Pet não aparece para o dono no grupo CARTAO")
	}
	if slices.ContainsFunc(paraA[Cartao], func(c Categoria) bool { return c.ID == "c2" }) {
		t.Error("categoria de B aparece para A")
	}
	if len(paraA[Cartao]) != 7 || len(paraA[Fixo]) != 6 {
		t.Errorf("A vê %d/%d", len(paraA[Cartao]), len(paraA[Fixo]))
	}
	if len(Visiveis(todas, "")) != 12 {
		t.Error("sem usuário só as padrão")
	}
}

// Cenário "Nome duplicado no grupo".
func TestNomeDuplicadoNoGrupo(t *testing.T) {
	pet := nova(t, "c1", "u-a", "Pet", Cartao)
	visiveis := append(Padrao(), pet)
	duplicados := []struct {
		nome  string
		grupo Grupo
	}{
		{"pet", Cartao},
		{"PET", Cartao},
		{"  pEt ", Cartao},
		{"alimentação", Cartao}, // igual a uma padrão
		{"ALIMENTAÇÃO", Cartao},
		{"condomínio", Fixo},
		{"Água", Fixo}, // "Água" com acento combinado
	}
	for _, d := range duplicados {
		n := nova(t, "c9", "u-a", d.nome, d.grupo)
		if err := ExigirNomeDisponivel(visiveis, n); !errors.Is(err, errs.Conflict(contract.CodigoDuplicada, "")) {
			t.Errorf("%q em %s: erro = %v", d.nome, d.grupo, err)
		}
	}

	disponiveis := []struct {
		nome  string
		grupo Grupo
		dono  string
	}{
		{"Pet", Fixo, "u-a"},           // mesmo nome em outro grupo
		{"Pet", Cartao, "u-b"},         // o Pet de A não é visível para B
		{"Alimentacao", Cartao, "u-a"}, // acento conta
		{"Aluguel", Cartao, "u-a"},     // Aluguel é do FIXO
		{"Pets", Cartao, "u-a"},
	}
	for _, d := range disponiveis {
		n := nova(t, "c9", d.dono, d.nome, d.grupo)
		if err := ExigirNomeDisponivel(visiveis, n); err != nil {
			t.Errorf("%q em %s por %s: %v", d.nome, d.grupo, d.dono, err)
		}
	}
}

func TestOrdenarAlfabeticoPortugues(t *testing.T) {
	cats := []Categoria{
		{ID: "3", Nome: "beleza"}, {ID: "2", Nome: "Água"}, {ID: "1", Nome: "Zoo"},
		{ID: "5", Nome: "Aluguel"}, {ID: "4", Nome: "agenda"}, {ID: "0", Nome: "Água"},
	}
	Ordenar(cats)
	var got []string
	for _, c := range cats {
		got = append(got, c.ID+":"+c.Nome)
	}
	quer := []string{"4:agenda", "0:Água", "2:Água", "5:Aluguel", "3:beleza", "1:Zoo"}
	if !slices.Equal(got, quer) {
		t.Errorf("ordem = %v, quer %v", got, quer)
	}
}

func TestPorGrupoIgnoraGrupoInvalidoEDevolveVazios(t *testing.T) {
	pg := PorGrupo([]Categoria{{ID: "x", Grupo: "OUTRO"}})
	if len(pg) != 2 || pg[Cartao] == nil || pg[Fixo] == nil || len(pg[Cartao])+len(pg[Fixo]) != 0 {
		t.Errorf("pg = %+v", pg)
	}
}

func TestContract(t *testing.T) {
	got := Padrao()[0].Contract()
	quer := contract.Categoria{ID: "alimentacao", Nome: "Alimentação", Grupo: "CARTAO", Cor: "#F97316", Padrao: true}
	if got != quer {
		t.Errorf("contract = %+v", got)
	}
	if nova(t, "c1", "u", "Pet", Fixo).Contract().Padrao {
		t.Error("personalizada saiu como padrão")
	}
}

// TestPadraoIgualAoSeed confere que a cópia das categorias padrão no seed do
// infra-base (ambiente local) é idêntica à lista deste serviço, que é a fonte
// da verdade. Só roda com o infra-base clonado ao lado deste repositório (o
// layout local); na esteira é pulado.
func TestPadraoIgualAoSeed(t *testing.T) {
	caminho := os.Getenv("INFRA_BASE_SEED")
	if caminho == "" {
		caminho = filepath.Join("..", "..", "..", "gestor-gastos-infra-base", "local", "seed", "dados.go")
	}
	b, err := os.ReadFile(caminho)
	if err != nil {
		t.Skipf("seed do infra-base não encontrado em %s", caminho)
	}
	linha := regexp.MustCompile(`\{"([a-z]+)", "([A-Z]+)", "([^"]+)", "(#[0-9A-Fa-f]{6})"\}`)
	var seed []Categoria
	for _, m := range linha.FindAllStringSubmatch(string(b), -1) {
		seed = append(seed, Categoria{ID: m[1], Dono: DonoSistema, Grupo: Grupo(m[2]), Nome: m[3], Cor: m[4]})
	}
	if !slices.Equal(seed, Padrao()) {
		t.Errorf("seed do infra-base diverge da fonte da verdade:\nseed    %+v\nserviço %+v", seed, Padrao())
	}
}
