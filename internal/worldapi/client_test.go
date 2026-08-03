package worldapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/khorost-space/space-lab/internal/worldapi"
)

// TestCreateObjectSendsTokenAndReturnsID: /objects закрыт objectAuth, и
// забытый заголовок даёт 401 посреди подъёма стека.
func TestCreateObjectSendsTokenAndReturnsID(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "019f-объект", "name": "vega-0"})
	}))
	defer srv.Close()

	id, err := worldapi.New(srv.URL, "секрет", srv.Client()).
		CreateObject(context.Background(), "vega-0", "student")
	if err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if id != "019f-объект" {
		t.Errorf("id = %q", id)
	}
	if gotAuth != "Bearer секрет" {
		t.Errorf("заголовок авторизации = %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"owner":"student"`) {
		t.Errorf("владелец не передан: %q", gotBody)
	}
}

// TestCreateObjectSurfacesStatus: 401 обязан быть отличим от сетевого сбоя —
// причина у них разная, и лечатся они по-разному.
func TestCreateObjectSurfacesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := worldapi.New(srv.URL, "не тот", srv.Client()).
		CreateObject(context.Background(), "vega-0", "student")
	if err == nil {
		t.Fatal("401 принят за успех")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("в ошибке нет статуса: %v", err)
	}
}

// TestShowcaseReadsCondition: витрина — источник наблюдаемого состояния для
// status и check.
func TestShowcaseReadsCondition(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/showcase/data" {
			t.Errorf("запрошен %q, ожидался /showcase/data", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "vega-0", "condition": "online",
			"last_sequence": int64(17), "served_version": "5fafbc183649",
		})
	}))
	defer srv.Close()

	v, err := worldapi.New(srv.URL, "", srv.Client()).Showcase(context.Background())
	if err != nil {
		t.Fatalf("Showcase: %v", err)
	}
	if v.Condition != "online" || v.ServedVersion != "5fafbc183649" || v.LastSequence != 17 {
		t.Errorf("витрина прочитана неверно: %+v", v)
	}
}
