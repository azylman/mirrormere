package adapters_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azylman/mirrormere/internal/api"
	"github.com/azylman/mirrormere/internal/tasks/adapters"
	"github.com/azylman/mirrormere/internal/testutil"
)

func TestTodoistAdapter_CompleteItem(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, itemID  string
		route         testutil.Route
		close, cancel bool
		wantErr       string
		wantErrIs     error
		wantCode      int
	}{
		{"204_no_content", "i1", testutil.Route{Status: 204}, false, false, "", nil, 0},
		{"200_ok", "i1", testutil.Route{Status: 200}, false, false, "", nil, 0},
		{"404_not_found", "i1", testutil.Route{Status: 404}, false, false, "", adapters.ErrUpstreamNotFound, 0},
		{"401_unauthorized", "i1", testutil.Route{Status: 401}, false, false, "", adapters.ErrUnauthorized, 0},
		{"500_error", "i1", testutil.Route{Status: 500}, false, false, "", nil, 500},
		{"empty_item_id", "", testutil.Route{}, false, false, "item ID is required", nil, 0},
		{"invalid_item_id", "bad/..", testutil.Route{}, false, false, "invalid item ID", nil, 0},
		{"network_error", "i1", testutil.Route{}, true, false, "todoist close request failed", nil, 0},
		{"canceled_context", "i1", testutil.Route{Status: 200}, false, true, "context canceled", nil, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := testutil.RouteServer(t, map[string]testutil.Route{"/api/v1/tasks/" + tc.itemID + "/close": tc.route})
			if tc.close {
				srv.Close()
			}
			ad, _ := adapters.NewTodoistAdapter(adapters.TodoistAdapterConfig{Token: "tok", BaseURL: srv.URL, Client: srv.Client()})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			err := ad.CompleteItem(ctx, tc.itemID)
			if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
				t.Fatalf("expected %v, got %v", tc.wantErrIs, err)
			} else if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("expected %q, got %v", tc.wantErr, err)
			}
			var httpErr api.UpstreamHTTPError
			if tc.wantCode != 0 && (!errors.As(err, &httpErr) || httpErr.Code != tc.wantCode) {
				t.Fatalf("expected code %d, got %v", tc.wantCode, err)
			}
		})
	}
}
