package controlplane

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxFactoryReadBytes = 2 << 20
const maxFactoryPatchBytes = 8 << 20

// The console reads the stable JSON evidence layout without linking the worker runtime.
const factoryRunFile = "run.json"
const factoryPatchFile = "changes.patch"
const factoryChangeDir = "changes"

type factoryRead struct {
	root      string
	token     string
	index     *sql.DB
	indexMu   sync.Mutex
	indexedAt map[string]time.Time
}

// ConfigureFactoryRead enables optional evidence views before Serve is called.
// The console only reads the factory volume; it never opens QMS authority state.
func (s *Server) ConfigureFactoryRead(root, tokenFile string) error {
	if root == "" && tokenFile == "" {
		return nil
	}
	if root == "" || tokenFile == "" {
		return errors.New("factory_read requires both root and token_file")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("factory_read root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return errors.New("factory_read root must be an existing directory")
	}
	credential, err := os.Lstat(tokenFile)
	if err != nil {
		return fmt.Errorf("factory_read token_file: %w", err)
	}
	if !credential.Mode().IsRegular() || credential.Mode().Perm()&0o007 != 0 || credential.Size() > 4096 {
		return errors.New("factory_read token_file must be a private regular file of at most 4096 bytes")
	}
	data, err := os.ReadFile(tokenFile)
	if err != nil {
		return fmt.Errorf("factory_read token_file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
		return errors.New("factory_read token must have at least 32 non-whitespace characters")
	}
	index, err := openFactoryReadIndex(filepath.Join(filepath.Dir(s.store.path), "factory-read-index.sqlite"))
	if err != nil {
		return fmt.Errorf("factory_read index: %w", err)
	}
	reader := &factoryRead{root: resolved, token: token, index: index, indexedAt: map[string]time.Time{}}
	if err := reader.refreshIndex("runs"); err != nil {
		index.Close()
		return fmt.Errorf("factory_read runs index: %w", err)
	}
	if err := reader.refreshIndex(factoryChangeDir); err != nil {
		index.Close()
		return fmt.Errorf("factory_read changes index: %w", err)
	}
	s.factoryRead = reader
	s.handler, err = s.routes()
	if err != nil {
		index.Close()
		s.factoryRead = nil
	}
	return err
}

func (s *Server) authorizeFactoryRead(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Vary", "Authorization")
		provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(s.factoryRead.token)) != 1 {
			response.Header().Set("WWW-Authenticate", `Bearer realm="ESF factory evidence"`)
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(response, request)
	}
}

func validFactoryID(id string) bool {
	return id != "" && id != "." && id != ".." && len(id) <= 128 && !strings.ContainsAny(id, "/\\\x00")
}

func (s *Server) readFactoryFile(path string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(s.factoryRead.root)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("evidence file is unavailable or exceeds the read limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("evidence file is unavailable or exceeds the read limit")
	}
	return data, nil
}

func (s *Server) readFactoryManifest(id string) (json.RawMessage, error) {
	if !validFactoryID(id) {
		return nil, os.ErrNotExist
	}
	data, err := s.readFactoryFile(filepath.Join("runs", id, factoryRunFile), maxFactoryReadBytes)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.RunID != id {
		return nil, errors.New("invalid run manifest")
	}
	return data, nil
}

func factoryReadError(response http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		http.Error(response, "not found", http.StatusNotFound)
		return
	}
	http.Error(response, "factory evidence unavailable", http.StatusInternalServerError)
}

func parseFactoryPage(request *http.Request) (int, int, error) {
	limit, offset := 20, 0
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, 0, errors.New("limit must be between 1 and 100")
		}
		limit = parsed
	}
	if raw := request.URL.Query().Get("cursor"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 1_000_000 {
			return 0, 0, errors.New("invalid cursor")
		}
		offset = parsed
	}
	return limit, offset, nil
}

func nextFactoryCursor(offset, count, total int) string {
	if offset+count >= total {
		return ""
	}
	return strconv.Itoa(offset + count)
}

func (s *Server) listFactoryRuns(response http.ResponseWriter, request *http.Request) {
	limit, offset, err := parseFactoryPage(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	ids, total, err := s.factoryRead.page("runs", limit, offset)
	if err != nil {
		factoryReadError(response, err)
		return
	}
	runs := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		manifest, err := s.readFactoryManifest(id)
		if err != nil {
			factoryReadError(response, err)
			return
		}
		runs = append(runs, manifest)
	}
	writeJSON(response, http.StatusOK, map[string]any{"runs": runs, "next_cursor": nextFactoryCursor(offset, len(runs), total)})
}

func (s *Server) getFactoryRun(response http.ResponseWriter, request *http.Request) {
	manifest, err := s.readFactoryManifest(request.PathValue("id"))
	if err != nil {
		factoryReadError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, manifest)
}

func (s *Server) getFactoryConditions(response http.ResponseWriter, request *http.Request) {
	manifest, err := s.readFactoryManifest(request.PathValue("id"))
	if err != nil {
		factoryReadError(response, err)
		return
	}
	var result struct {
		Conditions json.RawMessage `json:"conditions"`
	}
	if err := json.Unmarshal(manifest, &result); err != nil {
		factoryReadError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"conditions": result.Conditions})
}

func (s *Server) getFactoryQuality(response http.ResponseWriter, request *http.Request) {
	manifest, err := s.readFactoryManifest(request.PathValue("id"))
	if err != nil {
		factoryReadError(response, err)
		return
	}
	var result struct {
		Quality json.RawMessage `json:"quality"`
	}
	if err := json.Unmarshal(manifest, &result); err != nil {
		factoryReadError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"quality": result.Quality})
}

func (s *Server) getFactoryEvidence(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if _, err := s.readFactoryManifest(id); err != nil {
		factoryReadError(response, err)
		return
	}
	base := filepath.Join(s.factoryRead.root, "runs", id)
	paths := make([]string, 0, 64)
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		if len(paths) >= 1000 {
			return errors.New("too many evidence files")
		}
		relative, err := filepath.Rel(base, path)
		if err == nil {
			paths = append(paths, filepath.ToSlash(relative))
		}
		return err
	})
	if err != nil {
		factoryReadError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"paths": paths})
}

func (s *Server) getFactoryPatch(response http.ResponseWriter, request *http.Request) {
	manifest, err := s.readFactoryManifest(request.PathValue("id"))
	if err != nil {
		factoryReadError(response, err)
		return
	}
	var result struct {
		RunID string `json:"run_id"`
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal(manifest, &result); err != nil {
		factoryReadError(response, err)
		return
	}
	if result.Patch != factoryPatchFile {
		http.Error(response, "patch unavailable", http.StatusNotFound)
		return
	}
	data, err := s.readFactoryFile(filepath.Join("runs", result.RunID, result.Patch), maxFactoryPatchBytes)
	if err != nil {
		factoryReadError(response, err)
		return
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.Header().Set("Content-Disposition", `attachment; filename="changes.patch"`)
	_, _ = response.Write(data)
}

func (s *Server) listFactoryChanges(response http.ResponseWriter, request *http.Request) {
	limit, offset, err := parseFactoryPage(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	ids, total, err := s.factoryRead.page(factoryChangeDir, limit, offset)
	if err != nil {
		factoryReadError(response, err)
		return
	}
	changes := make([]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		change, err := s.readFactoryChange(id)
		if err != nil {
			factoryReadError(response, err)
			return
		}
		changes = append(changes, change)
	}
	writeJSON(response, http.StatusOK, map[string]any{"changes": changes, "next_cursor": nextFactoryCursor(offset, len(changes), total)})
}

func (s *Server) readFactoryChange(id string) (json.RawMessage, error) {
	if !validFactoryID(id) {
		return nil, os.ErrNotExist
	}
	data, err := s.readFactoryFile(filepath.Join(factoryChangeDir, id+".json"), maxFactoryReadBytes)
	if err != nil {
		return nil, err
	}
	var change struct {
		ChangeID string `json:"change_id"`
	}
	if err := json.Unmarshal(data, &change); err != nil || change.ChangeID != id {
		return nil, errors.New("invalid change document")
	}
	return data, nil
}

func (s *Server) getFactoryChange(response http.ResponseWriter, request *http.Request) {
	change, err := s.readFactoryChange(request.PathValue("id"))
	if err != nil {
		factoryReadError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, change)
}
