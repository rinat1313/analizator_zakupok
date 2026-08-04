package checklist

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// List — набор требований/проверок для анализа тендера.
type List struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Items       []Item `yaml:"items" json:"items"`
}

// Item — один пункт чек-листа.
type Item struct {
	ID          string   `yaml:"id" json:"id"`
	Title       string   `yaml:"title" json:"title"`
	Description string   `yaml:"description" json:"description"`
	Prompt      string   `yaml:"prompt" json:"prompt"`
	Keywords    []string `yaml:"keywords" json:"keywords"`
	MaxChunks   int      `yaml:"max_chunks" json:"max_chunks"` // сколько кусков брать под пункт
	Weight      float64  `yaml:"weight" json:"weight"`
}

// Load читает YAML чек-лист по id (имя файла без расширения).
func Load(dir, id string) (*List, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("checklist id is empty")
	}
	candidates := []string{
		filepath.Join(dir, id+".yaml"),
		filepath.Join(dir, id+".yml"),
	}
	var lastErr error
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			lastErr = err
			continue
		}
		var list List
		if err := yaml.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("parse checklist %s: %w", p, err)
		}
		if list.ID == "" {
			list.ID = id
		}
		if list.Name == "" {
			list.Name = id
		}
		if len(list.Items) == 0 {
			return nil, fmt.Errorf("checklist %s has no items", p)
		}
		for i := range list.Items {
			if list.Items[i].MaxChunks <= 0 {
				list.Items[i].MaxChunks = 3
			}
			if list.Items[i].Weight <= 0 {
				list.Items[i].Weight = 1
			}
			if list.Items[i].ID == "" {
				list.Items[i].ID = fmt.Sprintf("item_%d", i+1)
			}
		}
		return &list, nil
	}
	return nil, fmt.Errorf("checklist %q not found in %s: %v", id, dir, lastErr)
}

// ListIDs возвращает доступные id чек-листов.
func ListIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ext))
	}
	return ids, nil
}
