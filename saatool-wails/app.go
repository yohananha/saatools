package main

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dtylman/saatool/actions"
	"github.com/dtylman/saatool/ai"
	"github.com/dtylman/saatool/config"
	"github.com/dtylman/saatool/translation"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ─── Data Transfer Objects ──────────────────────────────────────────────────

// ProjectInfo is the lightweight view of a project sent to the frontend.
type ProjectInfo struct {
	Path         string `json:"path"`
	Name         string `json:"name"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	Genre        string `json:"genre"`
	WritingStyle string `json:"writingStyle"`
	SourceLang   string `json:"sourceLang"`
	TargetLang   string `json:"targetLang"`
	Total        int    `json:"total"`
	Translated   int    `json:"translated"`
}

// ParagraphInfo holds one paragraph's content and metadata.
type ParagraphInfo struct {
	Index        int    `json:"index"`
	Text         string `json:"text"`
	IsSource     bool   `json:"isSource"`
	Direction    string `json:"direction"`    // "ltr" or "rtl"
	Total        int    `json:"total"`
	ChapterStart bool   `json:"chapterStart"` // true = first paragraph of a new chapter/section
}

// Position represents the last reading position in a project.
type Position struct {
	Index      int  `json:"index"`
	SourceView bool `json:"sourceView"`
}

// Settings mirrors config.Options for the frontend.
type Settings struct {
	DeepSeekAPIKey     string `json:"deepSeekAPIKey"`
	TranslateAhead     int    `json:"translateAhead"`
	AppSize            int    `json:"appSize"`
	TranslationDocSize int    `json:"translationDocSize"`
	AutoProofread      bool   `json:"autoProofread"`
	SourceLanguage     string `json:"sourceLanguage"`
	TargetLanguage     string `json:"targetLanguage"`
	DarkMode           bool   `json:"darkMode"`
	FixModel           string `json:"fixModel"`
}

// BookDetailsInfo carries per-book metadata to and from the frontend.
type BookDetailsInfo struct {
	Title        string                  `json:"title"`
	Author       string                  `json:"author"`
	Genre        string                  `json:"genre"`
	Synopsis     string                  `json:"synopsis"`
	WritingStyle string                  `json:"writingStyle"`
	Characters   []translation.Character `json:"characters"`
}

// TranslationEvent is emitted when a paragraph is translated.
type TranslationEvent struct {
	ProjectPath string `json:"projectPath"`
	Index       int    `json:"index"`
	Text        string `json:"text"`
}

// ─── App ────────────────────────────────────────────────────────────────────

// App holds the Wails application state.
type App struct {
	ctx         context.Context
	mu          sync.Mutex
	projects    map[string]*translation.Project
	translators map[string]*ai.Translator
}

// NewApp creates a new App instance.
func NewApp() *App {
	return &App{
		projects:    make(map[string]*translation.Project),
		translators: make(map[string]*ai.Translator),
	}
}

// startup is called by Wails when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// ─── Project helpers ────────────────────────────────────────────────────────

func projectToInfo(path string, p *translation.Project) *ProjectInfo {
	translated := 0
	for _, para := range p.Target.Paragraphs {
		if para.Text != "" {
			translated++
		}
	}
	return &ProjectInfo{
		Path:         path,
		Name:         p.Name,
		Title:        p.Title,
		Author:       p.Author,
		Genre:        p.Genre,
		WritingStyle: p.WritingStyle,
		SourceLang:   p.Source.Language,
		TargetLang:   p.Target.Language,
		Total:      len(p.Source.Paragraphs),
		Translated: translated,
	}
}

func dirStr(d translation.Direction) string {
	if d == translation.RightToLeft {
		return "rtl"
	}
	return "ltr"
}

// getOrLoad returns a cached project or loads it from disk.
func (a *App) getOrLoad(projectPath string) (*translation.Project, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if p, ok := a.projects[projectPath]; ok {
		return p, nil
	}
	p, err := translation.LoadProject(projectPath)
	if err != nil {
		return nil, err
	}
	p.Normalize()
	a.projects[projectPath] = p
	return p, nil
}

// setupTranslator creates (or replaces) the translator for a project.
// Must be called with a.mu held.
func (a *App) setupTranslatorLocked(projectPath string, p *translation.Project) {
	t, err := ai.NewTranslator(p)
	if err != nil {
		log.Printf("could not create translator for %s: %v", projectPath, err)
		return
	}
	t.OnTranslationComplete = func(index int, text string) {
		runtime.EventsEmit(a.ctx, "translation:complete", TranslationEvent{
			ProjectPath: projectPath,
			Index:       index,
			Text:        text,
		})
	}
	a.translators[projectPath] = t
}

// ─── Public API ─────────────────────────────────────────────────────────────

// ListProjects returns all project files known to the config.
func (a *App) ListProjects() ([]ProjectInfo, error) {
	files, err := config.ListProjects()
	if err != nil {
		return nil, err
	}
	var result []ProjectInfo
	for _, f := range files {
		p, err := a.getOrLoad(f.Path)
		if err != nil {
			log.Printf("skipping %s: %v", f.Path, err)
			continue
		}
		result = append(result, *projectToInfo(f.Path, p))
	}
	return result, nil
}

// LoadProject loads a project into memory and sets up its translator.
func (a *App) LoadProject(projectPath string) (*ProjectInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	p, err := translation.LoadProject(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load project: %w", err)
	}
	p.Normalize()
	a.projects[projectPath] = p
	a.setupTranslatorLocked(projectPath, p)

	return projectToInfo(projectPath, p), nil
}

// ImportEPUB converts an EPUB file into a new project.
func (a *App) ImportEPUB(epubPath, from, to string) (*ProjectInfo, error) {
	p, err := actions.ImportEPUBFile(epubPath, from, to)
	if err != nil {
		return nil, fmt.Errorf("failed to import EPUB: %w", err)
	}

	// ImportEPUBFile may leave the project Name as a full path; strip to
	// base filename without extension so p.Save() builds a valid path.
	base := filepath.Base(epubPath)
	base = base[:len(base)-len(filepath.Ext(base))] // remove .epub
	p.SetName(base)

	savedPath, err := p.Save()
	if err != nil {
		return nil, fmt.Errorf("EPUB imported but save failed: %w", err)
	}

	// Extract and cache the cover image (best-effort — never fails the import)
	if data, mime := extractCoverFromEPUB(epubPath); len(data) > 0 {
		saveCoverForProject(savedPath, data, mime)
	}

	a.mu.Lock()
	a.projects[savedPath] = p
	a.setupTranslatorLocked(savedPath, p)
	a.mu.Unlock()

	return projectToInfo(savedPath, p), nil
}

// ImportProjectFile copies a .spz file into the projects directory and loads it.
func (a *App) ImportProjectFile(spzPath string) (*ProjectInfo, error) {
	p, err := translation.LoadProject(spzPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read project file: %w", err)
	}
	p.Normalize()

	savedPath, err := p.Save()
	if err != nil {
		return nil, fmt.Errorf("failed to save imported project: %w", err)
	}
	a.mu.Lock()
	a.projects[savedPath] = p
	a.setupTranslatorLocked(savedPath, p)
	a.mu.Unlock()

	return projectToInfo(savedPath, p), nil
}

// GetParagraph retrieves one paragraph (source or target) from a project.
func (a *App) GetParagraph(projectPath string, index int, source bool) (*ParagraphInfo, error) {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return nil, err
	}

	// Chapter structure is always defined by source paragraphs.
	srcPara, err := p.GetSourceParagraph(index)
	if err != nil {
		return nil, err
	}

	var text string
	var lang string
	if source {
		text = srcPara.Text
		lang = p.GetSourceLanguage()
	} else {
		tgtPara, tErr := p.GetTargetParagraph(index)
		if tErr != nil {
			return nil, tErr
		}
		text = tgtPara.Text
		lang = p.GetTargetLanguage()
	}

	return &ParagraphInfo{
		Index:        index,
		Text:         text,
		IsSource:     source,
		Direction:    dirStr(translation.GetTextDirection(lang)),
		Total:        len(p.Source.Paragraphs),
		ChapterStart: srcPara.IsChapterStart,
	}, nil
}

// GetParagraphsBatch returns up to count paragraphs starting at fromIndex.
// Used by the paged reader to fill a screen with as many paragraphs as fit.
func (a *App) GetParagraphsBatch(projectPath string, fromIndex, count int, isSource bool) ([]ParagraphInfo, error) {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return nil, err
	}

	var lang string
	if isSource {
		lang = p.GetSourceLanguage()
	} else {
		lang = p.GetTargetLanguage()
	}
	dir := dirStr(translation.GetTextDirection(lang))
	total := len(p.Source.Paragraphs)

	if fromIndex >= total {
		return nil, nil
	}
	end := fromIndex + count
	if end > total {
		end = total
	}

	result := make([]ParagraphInfo, 0, end-fromIndex)
	for i := fromIndex; i < end; i++ {
		// Always read source para — it carries IsChapterStart for all modes.
		srcPara, sErr := p.GetSourceParagraph(i)
		if sErr != nil {
			continue
		}

		var text string
		if isSource {
			text = srcPara.Text
		} else {
			tgtPara, tErr := p.GetTargetParagraph(i)
			if tErr == nil {
				text = tgtPara.Text
			}
		}

		result = append(result, ParagraphInfo{
			Index:        i,
			Text:         text,
			IsSource:     isSource,
			Direction:    dir,
			Total:        total,
			ChapterStart: srcPara.IsChapterStart,
		})
	}
	return result, nil
}

// SetTranslation persists a manual edit to a paragraph's translation.
func (a *App) SetTranslation(projectPath string, index int, text string) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	return p.SetTranslation(index, text)
}

// SaveProject writes a project to disk.
func (a *App) SaveProject(projectPath string) error {
	a.mu.Lock()
	p, ok := a.projects[projectPath]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("project not loaded: %s", projectPath)
	}
	_, err := p.Save()
	return err
}

// DeleteProject removes a project from disk and memory.
func (a *App) DeleteProject(projectPath string) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	if err := translation.DeleteProject(p); err != nil {
		return err
	}
	a.mu.Lock()
	delete(a.projects, projectPath)
	delete(a.translators, projectPath)
	a.mu.Unlock()
	return nil
}

// ExportProject saves a project to an arbitrary output path (e.g. the user chose via dialog).
func (a *App) ExportProject(projectPath, outputPath string) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("could not create export file: %w", err)
	}
	defer f.Close()

	_, err = p.SaveToWriter(f)
	return err
}

// ExportProjectText exports a project as plain text (source | target pairs).
func (a *App) ExportProjectText(projectPath, outputPath string) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("could not create export file: %w", err)
	}
	defer f.Close()

	for i := range p.Source.Paragraphs {
		src := p.Source.Paragraphs[i].Text
		var tgt string
		if i < len(p.Target.Paragraphs) {
			tgt = p.Target.Paragraphs[i].Text
		}
		_, _ = io.WriteString(f, src+"\n---\n"+tgt+"\n\n")
	}
	return nil
}

// GetLastPosition returns the last saved reading position in a project.
func (a *App) GetLastPosition(projectPath string) (*Position, error) {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return nil, err
	}
	return &Position{
		Index:      p.LastParagraphIndex,
		SourceView: p.LastSourceView,
	}, nil
}

// SavePosition stores the current reading position in the project (and saves to disk).
func (a *App) SavePosition(projectPath string, index int, sourceView bool) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	p.SetPosition(sourceView, index)
	_, err = p.Save()
	return err
}

// TranslateParagraph triggers AI translation of a single paragraph.
func (a *App) TranslateParagraph(projectPath string, index int) error {
	a.mu.Lock()
	t, ok := a.translators[projectPath]
	a.mu.Unlock()
	if !ok {
		if _, err := a.LoadProject(projectPath); err != nil {
			return err
		}
		a.mu.Lock()
		t = a.translators[projectPath]
		a.mu.Unlock()
	}
	go func() {
		if err := t.Translate(a.ctx, index); err != nil {
			log.Printf("translation error at %d: %v", index, err)
		}
	}()
	return nil
}

// maxConcurrentTranslations limits how many paragraphs are translated in parallel
// to avoid triggering DeepSeek rate-limits when TranslateAhead > 2.
const maxConcurrentTranslations = 2

// TranslateParagraphs triggers translation of several paragraphs starting at fromIndex.
// At most maxConcurrentTranslations goroutines run at the same time (Change 5).
func (a *App) TranslateParagraphs(projectPath string, fromIndex int) error {
	a.mu.Lock()
	t, ok := a.translators[projectPath]
	p, pok := a.projects[projectPath]
	a.mu.Unlock()

	if !ok || !pok {
		if _, err := a.LoadProject(projectPath); err != nil {
			return err
		}
		a.mu.Lock()
		t = a.translators[projectPath]
		p = a.projects[projectPath]
		a.mu.Unlock()
	}

	end := fromIndex + config.Options.TranslateAhead
	if end > len(p.Source.Paragraphs) {
		end = len(p.Source.Paragraphs)
	}

	// Buffered channel used as a counting semaphore: at most maxConcurrentTranslations
	// goroutines may call the DeepSeek API simultaneously.
	sem := make(chan struct{}, maxConcurrentTranslations)
	for i := fromIndex; i < end; i++ {
		idx := i
		sem <- struct{}{} // acquire slot; blocks when maxConcurrentTranslations are busy
		go func() {
			defer func() { <-sem }() // release slot when done
			if err := t.Translate(a.ctx, idx); err != nil {
				log.Printf("translation error at %d: %v", idx, err)
			}
		}()
	}
	return nil
}

// FixTranslation asks the AI to proofread/fix one paragraph.
func (a *App) FixTranslation(projectPath string, index int) error {
	a.mu.Lock()
	t, ok := a.translators[projectPath]
	a.mu.Unlock()
	if !ok {
		if _, err := a.LoadProject(projectPath); err != nil {
			return err
		}
		a.mu.Lock()
		t = a.translators[projectPath]
		a.mu.Unlock()
	}
	go func() {
		if err := t.FixTranslation(a.ctx, index); err != nil {
			log.Printf("fix translation error at %d: %v", index, err)
		}
	}()
	return nil
}

// ─── Book details ────────────────────────────────────────────────────────────

// GetBookDetailsInfo returns the current book metadata stored in the project (no AI call).
func (a *App) GetBookDetailsInfo(projectPath string) (*BookDetailsInfo, error) {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return nil, err
	}
	return &BookDetailsInfo{
		Title:        p.Title,
		Author:       p.Author,
		Genre:        p.Genre,
		Synopsis:     p.Synopsis,
		WritingStyle: p.WritingStyle,
		Characters:   p.Characters,
	}, nil
}

// FetchBookDetails calls the AI to enrich the book's metadata (author, genre,
// synopsis, writing style, characters) and saves the result to the project.
// The translator for this project is rebuilt so that its cached system prompts
// reflect the new details immediately.
func (a *App) FetchBookDetails(projectPath string) (*BookDetailsInfo, error) {
	// Ensure both project and translator are loaded.
	a.mu.Lock()
	t, tok := a.translators[projectPath]
	p, pok := a.projects[projectPath]
	a.mu.Unlock()

	if !tok || !pok {
		if _, err := a.LoadProject(projectPath); err != nil {
			return nil, fmt.Errorf("could not load project: %w", err)
		}
		a.mu.Lock()
		t = a.translators[projectPath]
		p = a.projects[projectPath]
		a.mu.Unlock()
	}
	if t == nil {
		return nil, fmt.Errorf("could not create translator — check API key in Settings")
	}

	bookDetails, err := t.GetBookDetails(a.ctx)
	if err != nil {
		return nil, fmt.Errorf("AI fetch failed: %w", err)
	}

	// Apply enriched details to the project.
	p.Author = bookDetails.Author
	p.Genre = bookDetails.Genre
	p.Synopsis = bookDetails.Synopsis
	p.WritingStyle = bookDetails.WritingStyle
	p.Characters = bookDetails.MainCharacters
	// Guard nil: always store a non-nil slice so the frontend never gets a
	// JSON null for the characters field when the AI returns no characters.
	if p.Characters == nil {
		p.Characters = []translation.Character{}
	}

	if _, err := p.Save(); err != nil {
		log.Printf("could not save project after AI fetch: %v", err)
	}

	// Rebuild the translator so its cached system prompts include the new data.
	a.mu.Lock()
	a.setupTranslatorLocked(projectPath, p)
	a.mu.Unlock()

	return &BookDetailsInfo{
		Title:        p.Title,
		Author:       p.Author,
		Genre:        p.Genre,
		Synopsis:     p.Synopsis,
		WritingStyle: p.WritingStyle,
		Characters:   p.Characters,
	}, nil
}

// SaveBookDetailsInfo persists manually entered book metadata and rebuilds the
// translator so the new details take effect on subsequent translation calls.
func (a *App) SaveBookDetailsInfo(projectPath string, info BookDetailsInfo) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}

	p.Author = info.Author
	p.Genre = info.Genre
	p.Synopsis = info.Synopsis
	p.WritingStyle = info.WritingStyle
	p.Characters = info.Characters

	if _, err := p.Save(); err != nil {
		return fmt.Errorf("could not save project: %w", err)
	}

	// Rebuild translator so cached prompts reflect the updated metadata.
	a.mu.Lock()
	a.setupTranslatorLocked(projectPath, p)
	a.mu.Unlock()
	return nil
}

// ─── Glossary ────────────────────────────────────────────────────────────────

// GetGlossary returns all glossary entries for a project.
func (a *App) GetGlossary(projectPath string) (map[string]string, error) {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return nil, err
	}
	return p.GetGlossary(), nil
}

// SetGlossaryEntry adds or updates a glossary entry, saves the project, and
// rebuilds the translator so the new term is used in subsequent translations.
func (a *App) SetGlossaryEntry(projectPath, sourceTerm, targetTerm string) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	p.SetGlossaryEntry(sourceTerm, targetTerm)
	if _, err := p.Save(); err != nil {
		return fmt.Errorf("could not save project: %w", err)
	}
	a.mu.Lock()
	a.setupTranslatorLocked(projectPath, p)
	a.mu.Unlock()
	return nil
}

// DeleteGlossaryEntry removes a glossary entry, saves the project, and
// rebuilds the translator so the removed term is no longer applied.
func (a *App) DeleteGlossaryEntry(projectPath, sourceTerm string) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	p.DeleteGlossaryEntry(sourceTerm)
	if _, err := p.Save(); err != nil {
		return fmt.Errorf("could not save project: %w", err)
	}
	a.mu.Lock()
	a.setupTranslatorLocked(projectPath, p)
	a.mu.Unlock()
	return nil
}

// ─── Bookmarks ───────────────────────────────────────────────────────────────

// GetBookmarks returns all bookmarks for a project.
func (a *App) GetBookmarks(projectPath string) ([]translation.Bookmark, error) {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return nil, err
	}
	bookmarks := p.GetBookmarks()
	if bookmarks == nil {
		bookmarks = []translation.Bookmark{}
	}
	return bookmarks, nil
}

// AddBookmark adds or updates a bookmark at the given paragraph index.
func (a *App) AddBookmark(projectPath string, index int, note string) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	p.AddBookmark(index, note)
	if _, err := p.Save(); err != nil {
		return fmt.Errorf("could not save project: %w", err)
	}
	return nil
}

// DeleteBookmark removes the bookmark at the given paragraph index.
func (a *App) DeleteBookmark(projectPath string, index int) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	p.DeleteBookmark(index)
	if _, err := p.Save(); err != nil {
		return fmt.Errorf("could not save project: %w", err)
	}
	return nil
}

// ─── Cover helpers ───────────────────────────────────────────────────────────

// coverPathForProject returns the filesystem path where the cover image is stored.
// It tries .jpg then .png.
func coverPathForProject(projectPath string) (string, bool) {
	stem := strings.TrimSuffix(projectPath, config.ProjectFileExt)
	for _, ext := range []string{".cover.jpg", ".cover.png", ".cover.jpeg"} {
		p := stem + ext
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// maxCoverBytes caps the size of a cover image we are willing to store.
// A malicious or corrupt EPUB could embed an enormous image; this prevents
// the app from exhausting storage or memory.
const maxCoverBytes = 10 << 20 // 10 MB

// saveCoverForProject writes cover bytes alongside the project file.
func saveCoverForProject(projectPath string, data []byte, mediaType string) {
	if len(data) > maxCoverBytes {
		log.Printf("cover image too large (%d bytes > %d limit), skipping", len(data), maxCoverBytes)
		return
	}
	ext := ".cover.jpg"
	if strings.Contains(mediaType, "png") {
		ext = ".cover.png"
	}
	stem := strings.TrimSuffix(projectPath, config.ProjectFileExt)
	dest := stem + ext
	if err := os.WriteFile(dest, data, 0644); err != nil {
		log.Printf("could not save cover for %s: %v", projectPath, err)
	}
}

// GetProjectCover returns the cover as a base64 data-URI, or "" if unavailable.
func (a *App) GetProjectCover(projectPath string) string {
	p, ok := coverPathForProject(projectPath)
	if !ok {
		return ""
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	mime := "image/jpeg"
	if strings.HasSuffix(p, ".png") {
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// ─── EPUB cover extraction ────────────────────────────────────────────────────

type epubContainer struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type epubPackage struct {
	Metadata struct {
		Metas []struct {
			Name    string `xml:"name,attr"`
			Content string `xml:"content,attr"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
}

// extractCoverFromEPUB opens an EPUB zip and returns the cover image bytes
// and MIME type. Returns nil, "" if no cover is found.
func extractCoverFromEPUB(epubPath string) ([]byte, string) {
	r, err := zip.OpenReader(epubPath)
	if err != nil {
		log.Printf("cover: could not open epub %s: %v", epubPath, err)
		return nil, ""
	}
	defer r.Close()

	// Helper: find a zip entry by normalised path
	findZip := func(name string) *zip.File {
		name = path.Clean(name)
		for _, f := range r.File {
			if path.Clean(f.Name) == name {
				return f
			}
		}
		return nil
	}

	readZip := func(f *zip.File) ([]byte, error) {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		// Limit reads to maxCoverBytes to prevent memory exhaustion from a
		// malicious/corrupt EPUB that declares a huge cover image.
		return io.ReadAll(io.LimitReader(rc, maxCoverBytes))
	}

	// 1. META-INF/container.xml → OPF path
	cf := findZip("META-INF/container.xml")
	if cf == nil {
		return nil, ""
	}
	containerData, err := readZip(cf)
	if err != nil {
		return nil, ""
	}
	var container epubContainer
	if err := xml.Unmarshal(containerData, &container); err != nil || len(container.Rootfiles) == 0 {
		return nil, ""
	}
	opfPath := container.Rootfiles[0].FullPath
	opfDir := path.Dir(opfPath)

	// 2. Parse OPF
	opfFile := findZip(opfPath)
	if opfFile == nil {
		return nil, ""
	}
	opfData, err := readZip(opfFile)
	if err != nil {
		return nil, ""
	}
	var pkg epubPackage
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return nil, ""
	}

	// 3. Find cover item ID from <meta name="cover" content="...">  (EPUB2)
	var coverID string
	for _, m := range pkg.Metadata.Metas {
		if strings.EqualFold(m.Name, "cover") {
			coverID = m.Content
			break
		}
	}

	// 4. Find the cover manifest item
	var coverHref, coverMime string
	for _, item := range pkg.Manifest.Items {
		// EPUB3: properties="cover-image"
		if strings.Contains(item.Properties, "cover-image") &&
			strings.HasPrefix(item.MediaType, "image/") {
			coverHref = item.Href
			coverMime = item.MediaType
			break
		}
		// EPUB2: id matches the cover meta
		if coverID != "" && item.ID == coverID &&
			strings.HasPrefix(item.MediaType, "image/") {
			coverHref = item.Href
			coverMime = item.MediaType
		}
	}
	if coverHref == "" {
		return nil, ""
	}

	// 5. Build the path inside the zip (relative to OPF directory)
	coverZipPath := coverHref
	if opfDir != "." && opfDir != "" {
		coverZipPath = opfDir + "/" + coverHref
	}
	imgFile := findZip(coverZipPath)
	if imgFile == nil {
		return nil, ""
	}
	data, err := readZip(imgFile)
	if err != nil {
		return nil, ""
	}
	return data, coverMime
}

// ─── Settings ───────────────────────────────────────────────────────────────

// GetSettings returns the current application settings.
func (a *App) GetSettings() Settings {
	return Settings{
		DeepSeekAPIKey:     config.Options.DeepSeekAPIKey,
		TranslateAhead:     config.Options.TranslateAhead,
		AppSize:            config.Options.AppSize,
		TranslationDocSize: config.Options.TranslationDocSize,
		AutoProofread:      config.Options.AutoProofread,
		SourceLanguage:     config.Options.SourceLanguage,
		TargetLanguage:     config.Options.TargetLanguage,
		DarkMode:           config.Options.DarkMode,
		FixModel:           config.Options.FixModel,
	}
}

// SaveSettings persists the settings and returns the updated values.
func (a *App) SaveSettings(s Settings) error {
	// Validate numeric bounds so a malformed request cannot set values that
	// crash the runtime (e.g. TranslateAhead=0 means no translation fires).
	if s.TranslateAhead < 1 || s.TranslateAhead > 50 {
		return fmt.Errorf("translateAhead must be between 1 and 50")
	}
	if s.AppSize < 8 || s.AppSize > 48 {
		return fmt.Errorf("appSize must be between 8 and 48")
	}
	if s.TranslationDocSize < 1 || s.TranslationDocSize > 20 {
		return fmt.Errorf("translationDocSize must be between 1 and 20")
	}
	if s.FixModel != "" && s.FixModel != "deepseek-chat" && s.FixModel != "deepseek-reasoner" {
		return fmt.Errorf("fixModel must be \"deepseek-chat\" or \"deepseek-reasoner\"")
	}
	config.Options.DeepSeekAPIKey = s.DeepSeekAPIKey
	config.Options.TranslateAhead = s.TranslateAhead
	config.Options.AppSize = s.AppSize
	config.Options.TranslationDocSize = s.TranslationDocSize
	config.Options.AutoProofread = s.AutoProofread
	config.Options.SourceLanguage = s.SourceLanguage
	config.Options.TargetLanguage = s.TargetLanguage
	config.Options.DarkMode = s.DarkMode
	config.Options.FixModel = s.FixModel
	return config.SaveOptions()
}

// ─── Log ────────────────────────────────────────────────────────────────────

// GetLog returns captured log lines (newest last).
func (a *App) GetLog() []string {
	return GetMemLogger().GetLines()
}

// ─── File dialogs ────────────────────────────────────────────────────────────

// OpenEPUBDialog shows a native open dialog filtered to EPUB files.
func (a *App) OpenEPUBDialog() string {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Import EPUB",
		Filters: []runtime.FileFilter{
			{DisplayName: "EPUB books (*.epub)", Pattern: "*.epub"},
		},
	})
	if err != nil {
		log.Printf("open epub dialog error: %v", err)
		return ""
	}
	return path
}

// OpenProjectDialog shows a native open dialog filtered to .spz files.
func (a *App) OpenProjectDialog() string {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Open Project",
		Filters: []runtime.FileFilter{
			{DisplayName: "SaaTool projects (*.spz)", Pattern: "*.spz"},
		},
	})
	if err != nil {
		log.Printf("open project dialog error: %v", err)
		return ""
	}
	return path
}

// SaveProjectDialog shows a native save dialog for exporting a project.
func (a *App) SaveProjectDialog(suggestedName string) string {
	if filepath.Ext(suggestedName) == "" {
		suggestedName += config.ProjectFileExt
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Export Project",
		DefaultFilename: suggestedName,
		Filters: []runtime.FileFilter{
			{DisplayName: "SaaTool projects (*.spz)", Pattern: "*.spz"},
		},
	})
	if err != nil {
		log.Printf("save project dialog error: %v", err)
		return ""
	}
	return path
}

// SaveTextDialog shows a native save dialog for exporting as plain text.
func (a *App) SaveTextDialog(suggestedName string) string {
	if filepath.Ext(suggestedName) == "" {
		suggestedName += ".txt"
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Export as Text",
		DefaultFilename: suggestedName,
		Filters: []runtime.FileFilter{
			{DisplayName: "Text files (*.txt)", Pattern: "*.txt"},
		},
	})
	if err != nil {
		log.Printf("save text dialog error: %v", err)
		return ""
	}
	return path
}
