package main

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dtylman/saatool/actions"
	"github.com/dtylman/saatool/ai"
	"github.com/dtylman/saatool/config"
	"github.com/dtylman/saatool/export"
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
	ReadAt       int    `json:"readAt"`   // last read paragraph index
	Direct       bool   `json:"direct"`   // true = imported for direct reading (no translation)
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
	DeepSeekAPIKey            string `json:"deepSeekAPIKey"`
	TranslateAhead            int    `json:"translateAhead"`
	AppSize                   int    `json:"appSize"`
	TranslationDocSize        int    `json:"translationDocSize"`
	AutoProofread             bool   `json:"autoProofread"`
	SourceLanguage            string `json:"sourceLanguage"`
	TargetLanguage            string `json:"targetLanguage"`
	DarkMode                  bool   `json:"darkMode"`
	FixModel                  string `json:"fixModel"`
	MaxConcurrentTranslations int    `json:"maxConcurrentTranslations"`
	TranslationBatchSize      int    `json:"translationBatchSize"`
	ProjectsDirectory        string `json:"projectsDirectory"`
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
	// translationSem is a shared semaphore that caps concurrent API calls across
	// ALL TranslateParagraphs invocations, preventing goroutine accumulation when
	// the user triggers rapid translate-ahead requests.
	translationSem chan struct{}

	activeMu          sync.Mutex
	activeProjectPath string
	projectCancel     map[string]context.CancelFunc
}

// NewApp creates a new App instance.
func NewApp() *App {
	return &App{
		projects:       make(map[string]*translation.Project),
		translators:    make(map[string]*ai.Translator),
		translationSem: make(chan struct{}, config.Options.MaxConcurrentTranslations),
		projectCancel:  make(map[string]context.CancelFunc),
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
		Total:        len(p.Source.Paragraphs),
		Translated:   translated,
		ReadAt:       p.LastParagraphIndex,
		Direct:       p.Direct,
	}
}

func dirStr(d translation.Direction) string {
	if d == translation.RightToLeft {
		return "rtl"
	}
	return "ltr"
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

// getOrLoad returns a cached project or loads it from disk.
func (a *App) getOrLoad(projectPath string) (*translation.Project, error) {
	// Defense-in-depth: validate path even though callers come from the Wails
	// RPC bridge (file-dialog paths), guarding against a compromised frontend.
	if err := validateProjectPath(projectPath); err != nil {
		return nil, fmt.Errorf("invalid project path: %w", err)
	}

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
	var saveCount int32
	t.OnTranslationComplete = func(index int, text string) {
		runtime.EventsEmit(a.ctx, "translation:complete", TranslationEvent{
			ProjectPath: projectPath,
			Index:       index,
			Text:        text,
		})
		// Auto-save every 5 translated paragraphs in parallel so translation is not blocked.
		if atomic.AddInt32(&saveCount, 1)%5 == 0 {
			go func() {
				if _, err := p.Save(); err != nil {
					log.Printf("auto-save error for %s: %v", projectPath, err)
				}
			}()
		}
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
// If direct is true the book is imported for reading as-is (no translation needed).
func (a *App) ImportEPUB(epubPath, from, to string, direct bool) (*ProjectInfo, error) {
	p, err := actions.ImportEPUBFile(epubPath, from, to, direct)
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

// ExportProjectTranslationOnly writes only the target (translated) paragraphs to a text file.
// Paragraphs are separated by blank lines; chapter starts can be marked with a line of dashes.
func (a *App) ExportProjectTranslationOnly(projectPath, outputPath string) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("could not create export file: %w", err)
	}
	defer f.Close()

	for i, para := range p.Target.Paragraphs {
		if i > 0 && para.IsChapterStart {
			_, _ = io.WriteString(f, "\n---\n\n")
		}
		_, _ = io.WriteString(f, para.Text)
		_, _ = io.WriteString(f, "\n\n")
	}
	return nil
}

// ExportProjectEPUB writes the project as an EPUB (translation + metadata) to the given path.
func (a *App) ExportProjectEPUB(projectPath, outputPath string) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	return export.ProjectToEPUB(p, outputPath)
}

// GetLastPosition returns the last saved reading position in a project.
// If the saved position is 0 and there is translated content, returns the
// last translated paragraph and target view so the book opens at the end of
// the translated part.
func (a *App) GetLastPosition(projectPath string) (*Position, error) {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return nil, err
	}
	index, sourceView := p.EffectiveOpenPosition()
	return &Position{
		Index:      index,
		SourceView: sourceView,
	}, nil
}

// SavePosition stores the current reading position in the project.
// Only updates the in-memory position — does NOT flush to disk here.
// Calling p.Save() on every page turn holds p.mutex for a full gzip+JSON
// write of the entire project, which stalls concurrent GetParagraphsBatch
// calls. Position is already persisted by the auto-save in
// OnTranslationComplete (every 5 paragraphs) and by the explicit Save.
func (a *App) SavePosition(projectPath string, index int, sourceView bool) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	p.SetPosition(sourceView, index)
	return nil
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

// SetActiveProject sets the project allowed to run translation (e.g. the book open in Reader).
// Any in-flight translation for other projects is cancelled; those projects are saved to .spz so progress is not lost.
// Pass "" when leaving the Reader.
func (a *App) SetActiveProject(projectPath string) {
	a.activeMu.Lock()
	a.activeProjectPath = projectPath
	var toSave []string
	for path, cancel := range a.projectCancel {
		if projectPath == "" || path != projectPath {
			cancel()
			delete(a.projectCancel, path)
			toSave = append(toSave, path)
		}
	}
	a.activeMu.Unlock()
	for _, path := range toSave {
		if err := a.SaveProject(path); err != nil {
			log.Printf("save project on cancel %s: %v", path, err)
		}
	}
}

// TranslateWholeBook translates the entire project from paragraph 0 to end in the background.
func (a *App) TranslateWholeBook(projectPath string) error {
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
	total := len(p.Source.Paragraphs)
	if total == 0 {
		return nil
	}
	if total <= len(p.Target.Paragraphs) {
		allDone := true
		for i := 0; i < total && allDone; i++ {
			if p.Target.Paragraphs[i].Text == "" {
				allDone = false
			}
		}
		if allDone {
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.activeMu.Lock()
	a.projectCancel[projectPath] = cancel
	a.activeMu.Unlock()
	batchSize := config.Options.TranslationBatchSize
	if batchSize < 1 {
		batchSize = 1
	}
	go func() {
		defer func() {
			a.activeMu.Lock()
			if c, ok := a.projectCancel[projectPath]; ok {
				c()
				delete(a.projectCancel, projectPath)
			}
			a.activeMu.Unlock()
		}()
		for i := 0; i < total; i += batchSize {
			select {
			case <-ctx.Done():
				return
			default:
			}
			batchEnd := i + batchSize
			if batchEnd > total {
				batchEnd = total
			}
			indices := make([]int, batchEnd-i)
			for j := range indices {
				indices[j] = i + j
			}
			select {
			case <-ctx.Done():
				return
			case a.translationSem <- struct{}{}:
			}
			func(batch []int) {
				defer func() { <-a.translationSem }()
				if err := t.TranslateAndProofReadBatch(ctx, batch); err != nil && !errors.Is(err, context.Canceled) {
					log.Printf("whole-book batch error (%v): %v", batch, err)
				}
			}(indices)
		}
	}()
	return nil
}

// TranslateParagraphs triggers batched translation starting at fromIndex. Uses a cancellable
// context so SetActiveProject can stop it when the user switches books.
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
	if end > len(p.Target.Paragraphs) {
		end = len(p.Target.Paragraphs)
	}
	allTranslated := true
	for i := fromIndex; i < end && allTranslated; i++ {
		if p.Target.Paragraphs[i].Text == "" {
			allTranslated = false
		}
	}
	if allTranslated && fromIndex < end {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.activeMu.Lock()
	a.projectCancel[projectPath] = cancel
	a.activeMu.Unlock()

	go func() {
		defer func() {
			a.activeMu.Lock()
			if c, ok := a.projectCancel[projectPath]; ok {
				c()
				delete(a.projectCancel, projectPath)
			}
			a.activeMu.Unlock()
		}()
		select {
		case <-ctx.Done():
			return
		case a.translationSem <- struct{}{}:
		}
		if err := t.Translate(ctx, fromIndex); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("translation error (current %d): %v", fromIndex, err)
		}
		<-a.translationSem

		batchSize := config.Options.TranslationBatchSize
		if batchSize < 1 {
			batchSize = 1
		}
		for i := fromIndex + 1; i < end; i += batchSize {
			select {
			case <-ctx.Done():
				return
			default:
			}
			batchEnd := i + batchSize
			if batchEnd > end {
				batchEnd = end
			}
			indices := make([]int, batchEnd-i)
			for j := range indices {
				indices[j] = i + j
			}
			select {
			case <-ctx.Done():
				return
			case a.translationSem <- struct{}{}:
			}
			go func(batch []int) {
				defer func() { <-a.translationSem }()
				if err := t.TranslateAndProofReadBatch(ctx, batch); err != nil && !errors.Is(err, context.Canceled) {
					log.Printf("batch translation error (%v): %v", batch, err)
				}
			}(indices)
		}
	}()
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
	// H-4: Run Fix through the shared semaphore so it competes for API slots
	// with background translation, preventing unbounded goroutine accumulation.
	// Semaphore send is inside a goroutine so the Wails call returns immediately.
	go func() {
		a.translationSem <- struct{}{}
		defer func() { <-a.translationSem }()
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
	// Match the handler-layer limit (50 chars = sanitizeGlossaryText cap).
	const maxTermLen = 50
	if sourceTerm == "" {
		return fmt.Errorf("term cannot be empty")
	}
	if len(sourceTerm) > maxTermLen || len(targetTerm) > maxTermLen {
		return fmt.Errorf("term exceeds maximum length of %d chars", maxTermLen)
	}
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
	// Validate index is within the actual paragraph range so out-of-range
	// bookmarks can never be stored (they would cause silent JS undefined
	// access when rendering the bookmark list).
	if index < 0 || index >= len(p.Source.Paragraphs) {
		return fmt.Errorf("bookmark index %d out of range (0–%d)", index, len(p.Source.Paragraphs)-1)
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
	// 0600 = owner read/write only; consistent with options.json permissions.
	if err := os.WriteFile(dest, data, 0600); err != nil {
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

	// Helper: find a zip entry by normalised path.
	// NOTE: identical closure exists in saatool-android/server/app.go — keep in sync.
	findZip := func(name string) *zip.File {
		name = path.Clean(name)
		// Reject path-traversal attempts (e.g. "../../etc/passwd")
		if strings.HasPrefix(name, "../") || name == ".." {
			return nil
		}
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

// maskAPIKey replaces all but the last 4 characters of an API key with '*'.
// This allows the frontend to confirm a key is set without exposing the full value.
// NOTE: identical copy exists in saatool-android/server/app.go — keep in sync.
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	return strings.Repeat("*", len(key)-4) + key[len(key)-4:]
}

// GetSettings returns the current application settings.
func (a *App) GetSettings() Settings {
	return Settings{
		// C-1: Return a masked key so the full value is never exposed via Wails IPC.
		// SaveSettings ignores submitted values that contain '*'.
		DeepSeekAPIKey:            maskAPIKey(config.Options.DeepSeekAPIKey),
		TranslateAhead:            config.Options.TranslateAhead,
		AppSize:                   config.Options.AppSize,
		TranslationDocSize:        config.Options.TranslationDocSize,
		AutoProofread:             config.Options.AutoProofread,
		SourceLanguage:            config.Options.SourceLanguage,
		TargetLanguage:            config.Options.TargetLanguage,
		DarkMode:                  config.Options.DarkMode,
		FixModel:                  config.Options.FixModel,
		MaxConcurrentTranslations: config.Options.MaxConcurrentTranslations,
		TranslationBatchSize:      config.Options.TranslationBatchSize,
		ProjectsDirectory:         config.Options.ProjectsDirectory,
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
	if s.MaxConcurrentTranslations < 1 || s.MaxConcurrentTranslations > 8 {
		return fmt.Errorf("maxConcurrentTranslations must be between 1 and 8")
	}
	if s.TranslationBatchSize < 1 || s.TranslationBatchSize > 10 {
		return fmt.Errorf("translationBatchSize must be between 1 and 10")
	}
	const maxSavedLangLen = 100
	if len(s.SourceLanguage) > maxSavedLangLen || len(s.TargetLanguage) > maxSavedLangLen {
		return fmt.Errorf("language field too long (max %d chars)", maxSavedLangLen)
	}
	// Validate projects directory: if set, must be a path we can create/use (avoids crash later in ProjectsDir).
	if dir := strings.TrimSpace(s.ProjectsDirectory); dir != "" {
		dir = filepath.Clean(dir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("cannot use folder: %w", err)
		}
	}
	// C-1: Only update the API key when the submitted value is not the masked
	// placeholder (i.e. the user actually typed a new key).
	if s.DeepSeekAPIKey != "" && !strings.Contains(s.DeepSeekAPIKey, "*") {
		config.Options.DeepSeekAPIKey = s.DeepSeekAPIKey
	}
	config.Options.TranslateAhead = s.TranslateAhead
	config.Options.AppSize = s.AppSize
	config.Options.TranslationDocSize = s.TranslationDocSize
	config.Options.AutoProofread = s.AutoProofread
	config.Options.SourceLanguage = strings.TrimSpace(s.SourceLanguage)
	config.Options.TargetLanguage = strings.TrimSpace(s.TargetLanguage)
	config.Options.DarkMode = s.DarkMode
	config.Options.FixModel = strings.TrimSpace(s.FixModel)
	config.Options.MaxConcurrentTranslations = s.MaxConcurrentTranslations
	config.Options.TranslationBatchSize = s.TranslationBatchSize
	config.Options.ProjectsDirectory = strings.TrimSpace(s.ProjectsDirectory)
	// Recreate semaphore when concurrency changes so new batches respect the
	// new limit immediately. Old goroutines hold the previous channel reference
	// and will release their tokens to it safely; the old channel is then GC'd.
	if cap(a.translationSem) != s.MaxConcurrentTranslations {
		a.mu.Lock()
		a.translationSem = make(chan struct{}, s.MaxConcurrentTranslations)
		a.mu.Unlock()
	}
	return config.SaveOptions()
}

// ─── Log ────────────────────────────────────────────────────────────────────

// GetLog returns captured log lines (newest last).
func (a *App) GetLog() []string {
	return GetMemLogger().GetLines()
}

// ─── File dialogs ────────────────────────────────────────────────────────────

// defaultExportDir returns a directory outside the projects folder for export save dialogs,
// so that exported files do not appear as duplicate entries in the library.
// Prefers user's Documents; falls back to home dir. Returns empty string if none exist.
func defaultExportDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	docs := filepath.Join(home, "Documents")
	if info, err := os.Stat(docs); err == nil && info.IsDir() {
		return docs
	}
	if info, err := os.Stat(home); err == nil && info.IsDir() {
		return home
	}
	return ""
}

// OpenFolderDialog shows a native directory picker and returns the selected path (empty if cancelled).
func (a *App) OpenFolderDialog() string {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Choose folder for books",
	})
	if err != nil {
		log.Printf("open folder dialog error: %v", err)
		return ""
	}
	return path
}

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
// Default directory is set outside the projects folder so exports do not appear in the library.
func (a *App) SaveProjectDialog(suggestedName string) string {
	if filepath.Ext(suggestedName) == "" {
		suggestedName += config.ProjectFileExt
	}
	opts := runtime.SaveDialogOptions{
		Title:           "Export Project",
		DefaultFilename: suggestedName,
		Filters: []runtime.FileFilter{
			{DisplayName: "SaaTool projects (*.spz)", Pattern: "*.spz"},
		},
	}
	if dir := defaultExportDir(); dir != "" {
		opts.DefaultDirectory = dir
	}
	path, err := runtime.SaveFileDialog(a.ctx, opts)
	if err != nil {
		log.Printf("save project dialog error: %v", err)
		return ""
	}
	return path
}

// SaveTextDialog shows a native save dialog for exporting as plain text.
// Default directory is set outside the projects folder.
func (a *App) SaveTextDialog(suggestedName string) string {
	if filepath.Ext(suggestedName) == "" {
		suggestedName += ".txt"
	}
	opts := runtime.SaveDialogOptions{
		Title:           "Export as Text",
		DefaultFilename: suggestedName,
		Filters: []runtime.FileFilter{
			{DisplayName: "Text files (*.txt)", Pattern: "*.txt"},
		},
	}
	if dir := defaultExportDir(); dir != "" {
		opts.DefaultDirectory = dir
	}
	path, err := runtime.SaveFileDialog(a.ctx, opts)
	if err != nil {
		log.Printf("save text dialog error: %v", err)
		return ""
	}
	return path
}

// SaveEPUBDialog shows a native save dialog for exporting as EPUB.
// Default directory is set outside the projects folder.
func (a *App) SaveEPUBDialog(suggestedName string) string {
	if filepath.Ext(suggestedName) == "" {
		suggestedName += ".epub"
	}
	opts := runtime.SaveDialogOptions{
		Title:           "Export as EPUB",
		DefaultFilename: suggestedName,
		Filters: []runtime.FileFilter{
			{DisplayName: "EPUB books (*.epub)", Pattern: "*.epub"},
		},
	}
	if dir := defaultExportDir(); dir != "" {
		opts.DefaultDirectory = dir
	}
	path, err := runtime.SaveFileDialog(a.ctx, opts)
	if err != nil {
		log.Printf("save epub dialog error: %v", err)
		return ""
	}
	return path
}
