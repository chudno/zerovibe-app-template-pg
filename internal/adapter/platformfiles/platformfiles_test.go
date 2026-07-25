// Тесты файлового клиента: КОНТРАКТ с платформой (presign PUT → загрузка байтов →
// presign GET) и локальный фолбэк без ключа. Это стык с платформой — если он поедет,
// в приложениях перестанут грузиться/показываться файлы.
package platformfiles

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSave_PresignThenPut: прод-флоу — клиент сначала просит presigned URL у платформы
// (/dev/files), затем PUT-ит байты на выданный URL и возвращает ключ платформы.
func TestSave_PresignThenPut(t *testing.T) {
	var (
		gotPresignBody map[string]string
		gotKey         string
		putBody        string
		putContentLen  int64
	)
	// Бэкенд заливки (presigned PUT-цель). Отдельный сервер — имитирует S3.
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("S3: метод = %s, ожидался PUT", r.Method)
		}
		b, _ := io.ReadAll(r.Body)
		putBody = string(b)
		putContentLen = r.ContentLength
		w.WriteHeader(http.StatusOK)
	}))
	defer s3.Close()

	// Платформенный API: на /dev/files отдаёт presigned PUT-URL (наш s3) и ключ.
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dev/files" || r.Method != http.MethodPost {
			t.Errorf("платформа: %s %s, ожидался POST /dev/files", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != "pk_secret" {
			t.Errorf("X-API-Key = %q", r.Header.Get("X-API-Key"))
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotPresignBody)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"upload_url": s3.URL + "/put-target",
			"key":        "proj-1/uuid-photo.png",
		})
	}))
	defer platform.Close()

	c := New(platform.URL+"/", "pk_secret", t.TempDir())
	content := "binary-bytes"
	key, err := c.Save(context.Background(), "photo.png", strings.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	if gotPresignBody["file_name"] != "photo.png" {
		t.Errorf("file_name в presign = %q, ожидался photo.png", gotPresignBody["file_name"])
	}
	gotKey = key
	if gotKey != "proj-1/uuid-photo.png" {
		t.Errorf("возвращённый ключ = %q, ожидался ключ платформы", gotKey)
	}
	if putBody != content {
		t.Errorf("на S3 ушло тело %q, ожидалось %q", putBody, content)
	}
	if putContentLen != int64(len(content)) {
		t.Errorf("Content-Length = %d, ожидался %d", putContentLen, len(content))
	}
}

// TestURL_AsksPlatformForGet: URL по ключу просит у платформы presigned GET.
func TestURL_AsksPlatformForGet(t *testing.T) {
	var gotBody map[string]string
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dev/files/get" || r.Method != http.MethodPost {
			t.Errorf("платформа: %s %s, ожидался POST /dev/files/get", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "https://s3.example/get/" + gotBody["key"]})
	}))
	defer platform.Close()

	c := New(platform.URL, "pk_secret", t.TempDir())
	url, err := c.URL(context.Background(), "proj-1/uuid-photo.png")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if gotBody["key"] != "proj-1/uuid-photo.png" {
		t.Errorf("key в запросе = %q", gotBody["key"])
	}
	if url != "https://s3.example/get/proj-1/uuid-photo.png" {
		t.Errorf("URL = %q", url)
	}
}

// TestURL_EmptyKey: пустой ключ → пустой URL без запроса к платформе.
func TestURL_EmptyKey(t *testing.T) {
	c := New("https://unused", "pk_secret", t.TempDir())
	url, err := c.URL(context.Background(), "")
	if err != nil || url != "" {
		t.Fatalf("пустой ключ: url=%q err=%v, ожидалось пусто/nil", url, err)
	}
}

// TestSave_NoKey_LocalFallback: без ключа платформы файл пишется на диск, ключ —
// "local/<имя>", URL — "/uploads/<имя>". HTTP к платформе не идёт.
func TestSave_NoKey_LocalFallback(t *testing.T) {
	var called bool
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer platform.Close()

	dir := t.TempDir()
	c := New(platform.URL, "", dir) // пустой ключ → фолбэк
	key, err := c.Save(context.Background(), "../evil/doc.txt", strings.NewReader("hello"), 5)
	if err != nil {
		t.Fatalf("Save local: %v", err)
	}
	if called {
		t.Error("без ключа HTTP-запрос на платформу идти не должен")
	}
	// path-traversal в имени не должен вырваться из каталога.
	if key != "local/doc.txt" {
		t.Errorf("ключ = %q, ожидался local/doc.txt (безопасное базовое имя)", key)
	}
	data, err := os.ReadFile(filepath.Join(dir, "doc.txt"))
	if err != nil || string(data) != "hello" {
		t.Errorf("файл на диске: data=%q err=%v", data, err)
	}
	if url, _ := c.URL(context.Background(), key); url != "/uploads/doc.txt" {
		t.Errorf("локальный URL = %q, ожидался /uploads/doc.txt", url)
	}
}
