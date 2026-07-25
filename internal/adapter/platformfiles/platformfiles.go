// Package platformfiles — хранение файлов через платформенный файловый API Zerovibe.
// Реализует порт usecase.FileStore. Платформа выдаёт presigned-URL на S3 (как с
// почтой: сами S3-креды приложению не передаются), приложение льёт/читает байты по
// этим URL. Локально (без ключа платформы) — фолбэк в каталог на диске, чтобы
// загрузка файлов работала в dev без настроенного S3.
package platformfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Client — файловое хранилище поверх платформенного API.
type Client struct {
	apiURL   string
	apiKey   string
	http     *http.Client
	localDir string // каталог фолбэка, когда платформа не настроена (локальная разработка)
	logf     func(string, ...any)
}

// New собирает клиент. apiURL/apiKey приходят из окружения (PLATFORM_API_URL/KEY) —
// те же, что у почты. localDir — куда складывать файлы в dev-режиме (без ключа).
func New(apiURL, apiKey, localDir string) *Client {
	return &Client{
		apiURL:   strings.TrimRight(apiURL, "/"),
		apiKey:   apiKey,
		http:     &http.Client{Timeout: 30 * time.Second},
		localDir: localDir,
		logf:     log.Printf,
	}
}

// configured сообщает, настроена ли платформа (есть ключ и адрес). Иначе — фолбэк.
func (c *Client) configured() bool { return c.apiKey != "" && c.apiURL != "" }

// Save сохраняет файл и возвращает его КЛЮЧ (приложение хранит ключ в своей записи).
// Прод: просит у платформы presigned PUT-URL и заливает на него байты. Dev (нет
// ключа): пишет в localDir и возвращает локальный ключ "local/<имя>".
func (c *Client) Save(ctx context.Context, fileName string, content io.Reader, size int64) (string, error) {
	if !c.configured() {
		return c.saveLocal(fileName, content)
	}

	// 1) presigned PUT-URL + ключ от платформы.
	up, err := c.requestUpload(ctx, fileName)
	if err != nil {
		return "", err
	}

	// 2) PUT байтов файла на выданный URL (напрямую в S3, минуя платформу).
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, up.UploadURL, content)
	if err != nil {
		return "", fmt.Errorf("platformfiles: put request: %w", err)
	}
	if size > 0 {
		req.ContentLength = size
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("platformfiles: upload: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("platformfiles: upload status %d", resp.StatusCode)
	}
	return up.Key, nil
}

// URL возвращает ссылку для показа/скачивания файла по его ключу. Прод: presigned
// GET-URL от платформы. Dev: путь "/uploads/<имя>", раздаётся приложением со static.
func (c *Client) URL(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", nil
	}
	if !c.configured() {
		return c.localURL(key), nil
	}
	body, _ := json.Marshal(map[string]string{"key": key})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+"/dev/files/get", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("platformfiles: get request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("platformfiles: get url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("platformfiles: get url status %d", resp.StatusCode)
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("platformfiles: decode get url: %w", err)
	}
	return out.URL, nil
}

// uploadResp — ответ платформы на запрос заливки.
type uploadResp struct {
	UploadURL string `json:"upload_url"`
	Key       string `json:"key"`
}

// requestUpload просит у платформы presigned PUT-URL и ключ для имени файла.
func (c *Client) requestUpload(ctx context.Context, fileName string) (uploadResp, error) {
	body, _ := json.Marshal(map[string]string{"file_name": fileName})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+"/dev/files", bytes.NewReader(body))
	if err != nil {
		return uploadResp{}, fmt.Errorf("platformfiles: upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return uploadResp{}, fmt.Errorf("platformfiles: request upload url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return uploadResp{}, fmt.Errorf("platformfiles: request upload url status %d", resp.StatusCode)
	}
	var out uploadResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return uploadResp{}, fmt.Errorf("platformfiles: decode upload url: %w", err)
	}
	return out, nil
}

// --- Локальный фолбэк (dev без платформы) ---

// saveLocal пишет файл в localDir под безопасным базовым именем и возвращает ключ
// "local/<имя>". Раздаётся приложением как /uploads/<имя> (см. localURL).
func (c *Client) saveLocal(fileName string, content io.Reader) (string, error) {
	name := safeBase(fileName)
	if err := os.MkdirAll(c.localDir, 0o755); err != nil {
		return "", fmt.Errorf("platformfiles: mkdir local: %w", err)
	}
	dst := filepath.Join(c.localDir, name)
	f, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("platformfiles: create local: %w", err)
	}
	defer f.Close()
	if _, err := io.Copy(f, content); err != nil {
		return "", fmt.Errorf("platformfiles: write local: %w", err)
	}
	c.logf("[platformfiles] платформа не настроена — файл сохранён локально: %s", dst)
	return "local/" + name, nil
}

// localURL переводит локальный ключ в путь, который приложение раздаёт со static.
func (c *Client) localURL(key string) string {
	return "/uploads/" + strings.TrimPrefix(key, "local/")
}

// safeBase возвращает безопасное базовое имя файла (без каталогов и "../").
func safeBase(name string) string {
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	name = path.Base(name)
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}
