// Package worldapi — узкий клиент platform-api для полигона.
//
// Ровно две операции: завести объект и прочитать витрину. Полигон не
// управляет миром — он его поднимает и смотрит, и любой третий метод здесь
// означал бы, что space-lab начал делать работу Mission Control.
package worldapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client — HTTP-клиент к platform-api полигона.
type Client struct {
	baseURL string
	token   string
	hc      *http.Client
}

// New собирает клиент. hc обязателен: полигон сам решает таймауты и
// транспорт (в частности, в тестах подставляет httptest.Server.Client()).
func New(baseURL, token string, hc *http.Client) *Client {
	return &Client{baseURL: baseURL, token: token, hc: hc}
}

// View — очищенное представление витрины, форма ответа /showcase/data.
type View struct {
	Name      string
	Condition string
	// LastSequence — номер последнего учтённого сигнала аппарата.
	LastSequence int64
	// LastSeen — время последнего учтённого сигнала. Указатель: nil честно
	// означает «сигналов не было», в отличие от нулевого времени.
	LastSeen *time.Time
	// ServedVersion — первые 12 hex-символов digest без префикса алгоритма,
	// как их отдаёт витрина. Клиент значение не достраивает и не сверяет —
	// сверка с ожидаемым digest остаётся задачей вызывающего кода.
	ServedVersion string
}

type createObjectRequest struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
}

type createObjectResponse struct {
	ID string `json:"id"`
}

// CreateObject заводит объект через POST {base}/objects и возвращает
// выданный платформой object_id.
func (c *Client) CreateObject(ctx context.Context, name, owner string) (string, error) {
	body, err := json.Marshal(createObjectRequest{Name: name, Owner: owner})
	if err != nil {
		return "", fmt.Errorf("собрать тело запроса /objects: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/objects", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("собрать запрос POST /objects: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST /objects: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("POST /objects: неожиданный статус %d", resp.StatusCode)
	}

	var out createObjectResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("разобрать ответ POST /objects: %w", err)
	}
	return out.ID, nil
}

type showcaseResponse struct {
	Name          string     `json:"name"`
	Condition     string     `json:"condition"`
	LastSequence  int64      `json:"last_sequence"`
	LastSeen      *time.Time `json:"last_seen"`
	ServedVersion string     `json:"served_version"`
}

// Showcase читает публичную витрину GET {base}/showcase/data.
//
// Без токена: витрина смонтирована без аутентификации (отдельный
// ingress-хост), и заголовок Authorization здесь — лишний шум, который
// платформа всё равно игнорирует.
func (c *Client) Showcase(ctx context.Context) (View, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/showcase/data", nil)
	if err != nil {
		return View{}, fmt.Errorf("собрать запрос GET /showcase/data: %w", err)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return View{}, fmt.Errorf("GET /showcase/data: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return View{}, fmt.Errorf("GET /showcase/data: неожиданный статус %d", resp.StatusCode)
	}

	var out showcaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return View{}, fmt.Errorf("разобрать ответ GET /showcase/data: %w", err)
	}
	// showcaseResponse и View совпадают полями один в один — ниже прямое
	// приведение типа, а не построчное копирование.
	return View(out), nil
}
