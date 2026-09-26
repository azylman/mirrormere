package tasks

import (
	"errors"
	"time"
)

// Standard error definitions for task and list operations.
var (
	ErrListNotFound = errors.New("list not found")
	ErrItemNotFound = errors.New("item not found")
)

// List represents a distinct task or checklist entity per SPEC-008 §Data Model.
type List struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Source    string    `json:"source"`
	Sections  []string  `json:"sections"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListItem represents an individual item or task belonging to a List per SPEC-008 §Data Model.
type ListItem struct {
	ID        string    `json:"id"`
	ListID    string    `json:"list_id"`
	Title     string    `json:"title"`
	Done      bool      `json:"done"`
	Section   string    `json:"section,omitempty"`
	Position  int       `json:"position"`
	Assignee  *string   `json:"assignee"`
	DueDate   *string   `json:"due_date"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TasksSnapshot represents the normalized payload published over the SSE bus (widget.update)
// and consumed by widgets per SPEC-008 §Wire Protocol.
type TasksSnapshot struct {
	List  List       `json:"list"`
	Items []ListItem `json:"items"`
}
