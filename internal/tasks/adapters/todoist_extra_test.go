package adapters_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azylman/mirrormere/internal/tasks/adapters"
)

func TestTodoistAdapter_ExtraCoverage(t *testing.T) {
	t.Parallel()

	// 1. Projects endpoint returns 500 error when searching by ProjectName
	t.Run("project_name_500_error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "server error", http.StatusInternalServerError)
		}))
		defer srv.Close()

		ad, _ := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
			ProjectName: "Family",
			Token:       "tok",
			BaseURL:     srv.URL,
			Client:      srv.Client(),
		})
		_, _, err := ad.FetchList(context.Background())
		if err == nil {
			t.Fatal("expected error on 500 from projects endpoint")
		}
	})

	// 2. Sections endpoint returns 500 error
	t.Run("sections_500_error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/projects/p-1":
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "p-1", "name": "P1"})
			case "/api/v1/sections":
				http.Error(w, "server error", http.StatusInternalServerError)
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		ad, _ := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
			ProjectID: "p-1",
			Token:     "tok",
			BaseURL:   srv.URL,
			Client:    srv.Client(),
		})
		_, _, err := ad.FetchList(context.Background())
		if err == nil {
			t.Fatal("expected error on 500 from sections endpoint")
		}
	})

	// 3. Task with boolean ID (triggers default branch in flexibleString)
	t.Run("task_with_bool_id", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/projects/p-bool":
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "p-bool", "name": "Bool"})
			case "/api/v1/sections":
				_ = json.NewEncoder(w).Encode([]any{})
			case "/api/v1/tasks":
				_ = json.NewEncoder(w).Encode([]map[string]any{
					{"id": true, "content": "Bool ID", "order": 1},
				})
			case "/api/v1/tasks/completed":
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		ad, _ := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
			ProjectID: "p-bool",
			Token:     "tok",
			BaseURL:   srv.URL,
			Client:    srv.Client(),
		})
		_, items, err := ad.FetchList(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(items) != 1 || items[0].ID != "true" {
			t.Errorf("expected item ID 'true', got %+v", items)
		}
	})

	// 4. Tasks endpoint returns 500 error
	t.Run("tasks_500_error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/projects/p-tasks-err":
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "p-tasks-err", "name": "Tasks Err"})
			case "/api/v1/sections":
				_ = json.NewEncoder(w).Encode([]any{})
			case "/api/v1/tasks":
				http.Error(w, "server error", http.StatusInternalServerError)
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()

		ad, _ := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{
			ProjectID: "p-tasks-err",
			Token:     "tok",
			BaseURL:   srv.URL,
			Client:    srv.Client(),
		})
		_, _, err := ad.FetchList(context.Background())
		if err == nil {
			t.Fatal("expected error on 500 from tasks endpoint")
		}
	})
}
