// Package memstore é um app.Store em memória com a mesma semântica do store
// DynamoDB. Só é usado pelos testes unitários de app, handler e cmd/local.
package memstore

import (
	"context"
	"sync"

	"github.com/Rossete-Inc/gestor-gastos-categories-service/internal/categoria"
)

// Store guarda as categorias por dono.
type Store struct {
	mu   sync.Mutex
	cats map[string]map[string]categoria.Categoria // dono -> id -> categoria
	// Err, se não nil, é devolvido por todas as operações.
	Err error
}

// New cria um Store vazio, sem as categorias padrão.
func New() *Store { return &Store{cats: map[string]map[string]categoria.Categoria{}} }

// ComPadrao cria um Store já com as categorias padrão, como depois do seed.
func ComPadrao() *Store {
	s := New()
	_ = s.GravarPadrao(context.Background(), categoria.Padrao())
	return s
}

// Visiveis implementa app.Store.
func (s *Store) Visiveis(_ context.Context, userID string) ([]categoria.Categoria, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	var out []categoria.Categoria
	for _, dono := range []string{categoria.DonoSistema, userID} {
		for _, c := range s.cats[dono] {
			out = append(out, c)
		}
	}
	return out, nil
}

// Obter implementa app.Store.
func (s *Store) Obter(_ context.Context, userID, id string) (categoria.Categoria, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return categoria.Categoria{}, s.Err
	}
	for _, dono := range []string{categoria.DonoSistema, userID} {
		if c, ok := s.cats[dono][id]; ok {
			return c, nil
		}
	}
	return categoria.Categoria{}, categoria.ErrNaoEncontrada()
}

// Criar implementa app.Store, com a mesma garantia de nome único por grupo
// entre as categorias do usuário.
func (s *Store) Criar(_ context.Context, c categoria.Categoria) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	for _, outra := range s.cats[c.Dono] {
		if outra.Grupo == c.Grupo && outra.ChaveNome() == c.ChaveNome() {
			return categoria.ErrDuplicada(c.Grupo)
		}
	}
	if s.cats[c.Dono] == nil {
		s.cats[c.Dono] = map[string]categoria.Categoria{}
	}
	s.cats[c.Dono][c.ID] = c
	return nil
}

// Excluir implementa app.Store.
func (s *Store) Excluir(_ context.Context, c categoria.Categoria) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	if _, ok := s.cats[c.Dono][c.ID]; !ok {
		return categoria.ErrNaoEncontrada()
	}
	delete(s.cats[c.Dono], c.ID)
	return nil
}

// ExcluirDoUsuario implementa app.Store.
func (s *Store) ExcluirDoUsuario(_ context.Context, userID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return 0, s.Err
	}
	n := len(s.cats[userID])
	delete(s.cats, userID)
	return n, nil
}

// GravarPadrao implementa app.Store.
func (s *Store) GravarPadrao(_ context.Context, cats []categoria.Categoria) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	if s.cats[categoria.DonoSistema] == nil {
		s.cats[categoria.DonoSistema] = map[string]categoria.Categoria{}
	}
	for _, c := range cats {
		s.cats[categoria.DonoSistema][c.ID] = c
	}
	return nil
}

// Total devolve quantas categorias o dono tem.
func (s *Store) Total(dono string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cats[dono])
}

// Gravar põe a categoria direto no store, sem validação. Serve para simular
// dados inconsistentes nos testes.
func (s *Store) Gravar(c categoria.Categoria) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cats[c.Dono] == nil {
		s.cats[c.Dono] = map[string]categoria.Categoria{}
	}
	s.cats[c.Dono][c.ID] = c
}
