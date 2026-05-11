// Sync-receiver — HTTP sidecar server. Accepts file uploads and writes them to /workspace; POST /reload executes the hot-reload command.
package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"os/exec"
)

const (
	defaultPort   = "9090"
	workspaceEnv  = "WORKSPACE"
	reloadEnv     = "RELOAD_COMMAND"
	defaultReload = "touch /workspace/.reload"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}
	workspace := os.Getenv(workspaceEnv)
	if workspace == "" {
		workspace = "/workspace"
	}
	reloadCmd := os.Getenv(reloadEnv)
	if reloadCmd == "" {
		reloadCmd = defaultReload
	}

	if err := os.MkdirAll(workspace, 0755); err != nil {
		log.Fatalf("mkdir %s: %v", workspace, err)
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// PUT /files/<path> — request body is the file content. Path is relative to workspace.
	http.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/files/")
		path = strings.TrimPrefix(path, "/")
		if path == "" || strings.Contains(path, "..") {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		fullPath := filepath.Join(workspace, path)
		dir := filepath.Dir(fullPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := os.WriteFile(fullPath, body, 0644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// POST /reload — execute the hot-reload command (from env or request body "command=...")
	http.HandleFunc("/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cmdStr := reloadCmd
		if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
			if err := r.ParseForm(); err == nil && r.Form.Get("command") != "" {
				cmdStr = r.Form.Get("command")
			}
		}
		cmd := exec.Command("sh", "-c", cmdStr)
		cmd.Dir = workspace
		cmd.Env = append(os.Environ(), "WORKSPACE="+workspace)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			log.Printf("reload command failed: %v\n%s", err, out.Bytes())
			http.Error(w, out.String(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(out.Bytes())
	})

	// POST /sync — multipart/form-data: multiple files (key = relative path, value = content)
	http.HandleFunc("/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			path := part.FormName()
			if path == "" {
				path = part.FileName()
			}
			path = strings.TrimPrefix(path, "/")
			if path == "" || strings.Contains(path, "..") {
				continue
			}
			fullPath := filepath.Join(workspace, path)
			dir := filepath.Dir(fullPath)
			os.MkdirAll(dir, 0755)
			body, _ := io.ReadAll(part)
			os.WriteFile(fullPath, body, 0644)
		}
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("sync-receiver listening on :%s workspace=%s", port, workspace)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
