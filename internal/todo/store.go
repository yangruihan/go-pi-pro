package todo

import (
	"fmt"
	"strings"
)

type Status string

const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusSkipped    Status = "skipped"
	StatusBlocked    Status = "blocked"
)

type Item struct {
	ID     int
	Title  string
	Status Status
}

type Store struct {
	items []Item
}

func New() *Store {
	return &Store{items: make([]Item, 0)}
}

func (s *Store) Upsert(title string, status Status) Item {
	title = strings.TrimSpace(title)
	if title == "" {
		return Item{}
	}
	for i := range s.items {
		if strings.EqualFold(s.items[i].Title, title) {
			s.items[i].Status = status
			return s.items[i]
		}
	}
	item := Item{ID: len(s.items) + 1, Title: title, Status: status}
	s.items = append(s.items, item)
	return item
}

func (s *Store) All() []Item {
	out := make([]Item, len(s.items))
	copy(out, s.items)
	return out
}

func (s *Store) Render() string {
	if len(s.items) == 0 {
		return "(no todos)"
	}
	var b strings.Builder
	for _, it := range s.items {
		_, _ = fmt.Fprintf(&b, "- [%s] %d. %s\n", it.Status, it.ID, it.Title)
	}
	return strings.TrimSpace(b.String())
}
