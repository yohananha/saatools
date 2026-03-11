package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dtylman/saatool/config"
)

// appTempDir returns a writable temp directory.
// On Android, os.TempDir() → /data/local/tmp which is not app-accessible.
// We use $FILESDIR/tmp instead, which is inside the app's sandboxed storage.
func appTempDir() string {
	if d := os.Getenv("FILESDIR"); d != "" {
		tmp := filepath.Join(d, "tmp")
		_ = os.MkdirAll(tmp, 0755)
		return tmp
	}
	return "" // falls back to os.TempDir() on desktop
}

// ─── Encoding helpers ─────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON error: %v", err)
	}
}

func writeErr(w http.ResponseWriter, err error, code int) {
	http.Error(w, err.Error(), code)
}

func decodeBody(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func queryStr(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}

func queryInt(r *http.Request, key string) (int, error) {
	return strconv.Atoi(queryStr(r, key))
}

func queryBool(r *http.Request, key string) bool {
	v := queryStr(r, key)
	return v == "true" || v == "1"
}

// validateProjectPath returns an error if projectPath would escape the projects
// directory. Prevents path-traversal attacks (../../etc/passwd, etc.).
func validateProjectPath(projectPath string) error {
	if projectPath == "" {
		return fmt.Errorf("project path is empty")
	}
	projDir := filepath.Clean(config.ProjectsDir())
	clean := filepath.Clean(projectPath)
	rel, err := filepath.Rel(projDir, clean)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." {
		return fmt.Errorf("invalid project path: must be within projects directory")
	}
	return nil
}

// ─── Settings ────────────────────────────────────────────────────────────────

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.app.GetSettings())
	case http.MethodPost:
		var settings Settings
		if err := decodeBody(r, &settings); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		if err := s.app.SaveSettings(settings); err != nil {
			writeErr(w, err, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}

// ─── Projects ────────────────────────────────────────────────────────────────

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	projects, err := s.app.ListProjects()
	if err != nil {
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []ProjectInfo{}
	}
	writeJSON(w, projects)
}

func (s *Server) handleLoadProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if err := validateProjectPath(body.Path); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	info, err := s.app.LoadProject(body.Path)
	if err != nil {
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, info)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if err := validateProjectPath(body.Path); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if err := s.app.DeleteProject(body.Path); err != nil {
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSaveProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if err := validateProjectPath(body.Path); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if err := s.app.SaveProject(body.Path); err != nil {
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleImportEPUB receives a multipart upload, saves to a temp file,
// calls ImportEPUB, then removes the temp file.
func (s *Server) handleImportEPUB(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseMultipartForm(64 << 20); err != nil { // 64 MB
		writeErr(w, fmt.Errorf("could not parse form: %w", err), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, fmt.Errorf("missing 'file' field: %w", err), http.StatusBadRequest)
		return
	}
	defer file.Close()

	sourceLang := r.FormValue("sourceLang")
	targetLang := r.FormValue("targetLang")

	// Save to temp file preserving .epub extension
	tmp, err := os.CreateTemp(appTempDir(), "import-*"+filepath.Ext(header.Filename))
	if err != nil {
		writeErr(w, fmt.Errorf("could not create temp file: %w", err), http.StatusInternalServerError)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		writeErr(w, fmt.Errorf("could not write temp file: %w", err), http.StatusInternalServerError)
		return
	}
	tmp.Close()

	info, err := s.app.ImportEPUB(tmpPath, sourceLang, targetLang)
	if err != nil {
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, info)
}

func (s *Server) handleImportSPZ(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, fmt.Errorf("could not parse form: %w", err), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, fmt.Errorf("missing 'file' field: %w", err), http.StatusBadRequest)
		return
	}
	defer file.Close()

	tmp, err := os.CreateTemp(appTempDir(), "import-*"+filepath.Ext(header.Filename))
	if err != nil {
		writeErr(w, fmt.Errorf("could not create temp file: %w", err), http.StatusInternalServerError)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	tmp.Close()

	info, err := s.app.ImportProjectFile(tmpPath)
	if err != nil {
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, info)
}

func (s *Server) handleExportProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	projectPath := queryStr(r, "path")
	if err := validateProjectPath(projectPath); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}

	// Stream the project file directly to the browser as a download.
	name := filepath.Base(projectPath)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Header().Set("Content-Type", "application/octet-stream")

	if err := s.app.WriteProjectTo(projectPath, w); err != nil {
		// Headers already sent, so just log.
		log.Printf("export error for %s: %v", projectPath, err)
	}
}

func (s *Server) handleProjectCover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	projectPath := queryStr(r, "path")
	if err := validateProjectPath(projectPath); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	cover := s.app.GetProjectCover(projectPath)
	writeJSON(w, cover)
}

// ─── Paragraphs ──────────────────────────────────────────────────────────────

func (s *Server) handleParagraphsBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	projectPath := queryStr(r, "path")
	if err := validateProjectPath(projectPath); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	from, err := queryInt(r, "from")
	if err != nil {
		writeErr(w, fmt.Errorf("invalid 'from': %w", err), http.StatusBadRequest)
		return
	}
	if from < 0 {
		writeErr(w, fmt.Errorf("'from' must be >= 0"), http.StatusBadRequest)
		return
	}
	count, err := queryInt(r, "count")
	if err != nil {
		writeErr(w, fmt.Errorf("invalid 'count': %w", err), http.StatusBadRequest)
		return
	}
	// Cap batch size to prevent DoS via count=999999999 causing memory exhaustion.
	const maxBatchCount = 500
	if count < 1 || count > maxBatchCount {
		writeErr(w, fmt.Errorf("'count' must be between 1 and %d", maxBatchCount), http.StatusBadRequest)
		return
	}
	isSource := queryBool(r, "source")

	paras, err := s.app.GetParagraphsBatch(projectPath, from, count, isSource)
	if err != nil {
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	if paras == nil {
		paras = []ParagraphInfo{}
	}
	writeJSON(w, paras)
}

func (s *Server) handlePosition(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projectPath := queryStr(r, "path")
		if err := validateProjectPath(projectPath); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		pos, err := s.app.GetLastPosition(projectPath)
		if err != nil {
			writeErr(w, err, http.StatusInternalServerError)
			return
		}
		writeJSON(w, pos)

	case http.MethodPost:
		var body struct {
			Path       string `json:"path"`
			Index      int    `json:"index"`
			SourceView bool   `json:"sourceView"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		if err := validateProjectPath(body.Path); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		if err := s.app.SavePosition(body.Path, body.Index, body.SourceView); err != nil {
			writeErr(w, err, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Path string `json:"path"`
		From int    `json:"from"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if err := validateProjectPath(body.Path); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if err := s.app.TranslateParagraphs(body.Path, body.From); err != nil {
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFixTranslation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Path  string `json:"path"`
		Index int    `json:"index"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if err := validateProjectPath(body.Path); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if err := s.app.FixTranslation(body.Path, body.Index); err != nil {
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Book details ─────────────────────────────────────────────────────────────

func (s *Server) handleBook(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projectPath := queryStr(r, "path")
		if err := validateProjectPath(projectPath); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		info, err := s.app.GetBookDetailsInfo(projectPath)
		if err != nil {
			writeErr(w, err, http.StatusInternalServerError)
			return
		}
		writeJSON(w, info)

	case http.MethodPost:
		var body struct {
			Path string `json:"path"`
			BookDetailsInfo
		}
		if err := decodeBody(r, &body); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		if err := validateProjectPath(body.Path); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		if err := s.app.SaveBookDetailsInfo(body.Path, body.BookDetailsInfo); err != nil {
			writeErr(w, err, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleFetchBookDetails(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	if err := validateProjectPath(body.Path); err != nil {
		writeErr(w, err, http.StatusBadRequest)
		return
	}
	info, err := s.app.FetchBookDetails(body.Path)
	if err != nil {
		writeErr(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, info)
}

// ─── Glossary ────────────────────────────────────────────────────────────────

func (s *Server) handleGlossary(w http.ResponseWriter, r *http.Request) {
	// Support X-HTTP-Method override (from api/index.js DELETE-as-POST pattern).
	method := r.Method
	if override := r.Header.Get("X-HTTP-Method"); override != "" {
		method = override
	}

	switch method {
	case http.MethodGet:
		projectPath := queryStr(r, "path")
		if err := validateProjectPath(projectPath); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		glossary, err := s.app.GetGlossary(projectPath)
		if err != nil {
			writeErr(w, err, http.StatusInternalServerError)
			return
		}
		if glossary == nil {
			glossary = map[string]string{}
		}
		writeJSON(w, glossary)

	case http.MethodPost:
		var body struct {
			Path       string `json:"path"`
			Term       string `json:"term"`
			TargetTerm string `json:"targetTerm"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		if err := validateProjectPath(body.Path); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		// Prevent empty terms and enforce a reasonable length limit to reduce
		// the surface area for AI prompt injection via crafted glossary entries.
		// Matches sanitizeGlossaryText's 50-char cap in project.go.
		const maxTermLen = 50
		if body.Term == "" {
			writeErr(w, fmt.Errorf("term cannot be empty"), http.StatusBadRequest)
			return
		}
		if len(body.Term) > maxTermLen || len(body.TargetTerm) > maxTermLen {
			writeErr(w, fmt.Errorf("term exceeds maximum length of %d chars", maxTermLen), http.StatusBadRequest)
			return
		}
		if err := s.app.SetGlossaryEntry(body.Path, body.Term, body.TargetTerm); err != nil {
			writeErr(w, err, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case http.MethodDelete:
		var body struct {
			Path string `json:"path"`
			Term string `json:"term"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		if err := validateProjectPath(body.Path); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		if err := s.app.DeleteGlossaryEntry(body.Path, body.Term); err != nil {
			writeErr(w, err, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.NotFound(w, r)
	}
}

// ─── Bookmarks ───────────────────────────────────────────────────────────────

func (s *Server) handleBookmarks(w http.ResponseWriter, r *http.Request) {
	method := r.Method
	if override := r.Header.Get("X-HTTP-Method"); override != "" {
		method = override
	}

	switch method {
	case http.MethodGet:
		projectPath := queryStr(r, "path")
		if err := validateProjectPath(projectPath); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		bookmarks, err := s.app.GetBookmarks(projectPath)
		if err != nil {
			writeErr(w, err, http.StatusInternalServerError)
			return
		}
		writeJSON(w, bookmarks)

	case http.MethodPost:
		var body struct {
			Path  string `json:"path"`
			Index int    `json:"index"`
			Note  string `json:"note"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		if err := validateProjectPath(body.Path); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		if body.Index < 0 {
			writeErr(w, fmt.Errorf("bookmark index cannot be negative"), http.StatusBadRequest)
			return
		}
		// Note length cap: guard against project file bloat.
		const maxNoteLen = 1000
		if len(body.Note) > maxNoteLen {
			writeErr(w, fmt.Errorf("note exceeds maximum length of %d characters", maxNoteLen), http.StatusBadRequest)
			return
		}
		if err := s.app.AddBookmark(body.Path, body.Index, body.Note); err != nil {
			writeErr(w, err, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case http.MethodDelete:
		var body struct {
			Path  string `json:"path"`
			Index int    `json:"index"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		if err := validateProjectPath(body.Path); err != nil {
			writeErr(w, err, http.StatusBadRequest)
			return
		}
		if err := s.app.DeleteBookmark(body.Path, body.Index); err != nil {
			writeErr(w, err, http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.NotFound(w, r)
	}
}

// ─── Log ─────────────────────────────────────────────────────────────────────

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	lines := s.app.GetLog()
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, lines)
}
