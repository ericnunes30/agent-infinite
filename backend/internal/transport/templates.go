package transport

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/agent-infinite/agent-infinite/backend/internal/contracts"
)

type templateStore struct {
	mu        sync.RWMutex
	path      string
	templates []contracts.TeamTemplate
}

func newTemplateStore(path string) *templateStore {
	store := &templateStore{path: path, templates: []contracts.TeamTemplate{}}
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &store.templates)
	}
	return store
}

func (s *templateStore) List() []contracts.TeamTemplate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneTemplates(s.templates)
}

func (s *templateStore) Save(template contracts.TeamTemplate) (contracts.TeamTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if template.ID == "" {
		template.ID = templateID()
		template.CreatedAt = time.Now().UTC()
	}
	s.templates = append(s.templates, template)
	if err := s.persist(); err != nil {
		s.templates = s.templates[:len(s.templates)-1]
		return contracts.TeamTemplate{}, err
	}
	return template, nil
}

func (s *templateStore) Update(id string, replacement contracts.TeamTemplate) (contracts.TeamTemplate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, current := range s.templates {
		if current.ID != id {
			continue
		}
		replacement.ID = current.ID
		replacement.CreatedAt = current.CreatedAt
		previous := current
		s.templates[index] = replacement
		if err := s.persist(); err != nil {
			s.templates[index] = previous
			return contracts.TeamTemplate{}, err
		}
		return replacement, nil
	}
	return contracts.TeamTemplate{}, errors.New("template not found")
}

func (s *templateStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for i, item := range s.templates {
		if item.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("template not found")
	}
	previous := s.templates[index]
	s.templates = append(s.templates[:index], s.templates[index+1:]...)
	if err := s.persist(); err != nil {
		s.templates = append(s.templates[:index], append([]contracts.TeamTemplate{previous}, s.templates[index:]...)...)
		return err
	}
	return nil
}

func (s *templateStore) Find(id string) (contracts.TeamTemplate, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.templates {
		if item.ID == id {
			return item, true
		}
	}
	return contracts.TeamTemplate{}, false
}

func (s *templateStore) persist() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.templates, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o600)
}

func cloneTemplates(source []contracts.TeamTemplate) []contracts.TeamTemplate {
	result := make([]contracts.TeamTemplate, len(source))
	for i, template := range source {
		result[i] = template
		result[i].Nodes = make([]contracts.Node, len(template.Nodes))
		result[i].Edges = make([]contracts.Edge, len(template.Edges))
		copy(result[i].Nodes, template.Nodes)
		copy(result[i].Edges, template.Edges)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

func templateID() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}
