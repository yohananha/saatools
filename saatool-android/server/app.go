package server

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
)

// ─── Data Transfer Objects ───────────────────────────────────────────────────

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
	Direction    string `json:"direction"`
	Total        int    `json:"total"`
	ChapterStart bool   `json:"chapterStart"`
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

// TranslationEvent is sent to WebSocket clients when a paragraph is translated.
type TranslationEvent struct {
	ProjectPath string `json:"projectPath"`
	Index       int    `json:"index"`
	Text        string `json:"text"`
}

// ─── App ─────────────────────────────────────────────────────────────────────

// BroadcastFn is called each time a paragraph translation completes.
type BroadcastFn func(ev TranslationEvent)

// App holds the application state; a port of saatool-wails/app.go without Wails.
type App struct {
	ctx            context.Context
	mu             sync.Mutex
	projects       map[string]*translation.Project
	translators    map[string]*ai.Translator
	broadcast      BroadcastFn
	// translationSem is a shared semaphore that caps concurrent API calls across
	// ALL TranslateParagraphs invocations, preventing goroutine accumulation when
	// the user triggers rapid translate-ahead requests.
	translationSem chan struct{}
	// fixSem is a dedicated slot for Fix so it runs immediately without waiting behind batch translation.
	fixSem chan struct{}

	// activeProjectPath is the project currently allowed to run translation (Reader open or whole-book from Library).
	// SetActiveProject cancels any other project's in-flight translation.
	activeMu         sync.Mutex
	activeProjectPath string
	projectCancel    map[string]context.CancelFunc
}

// newApp creates a new App instance.
func newApp(broadcast BroadcastFn) *App {
	return &App{
		ctx:              context.Background(),
		projects:         make(map[string]*translation.Project),
		translators:      make(map[string]*ai.Translator),
		broadcast:        broadcast,
		translationSem:   make(chan struct{}, config.Options.MaxConcurrentTranslations),
		fixSem:           make(chan struct{}, 1),
		projectCancel:    make(map[string]context.CancelFunc),
	}
}

// ─── Internal helpers ────────────────────────────────────────────────────────

func projectToInfo(projectPath string, p *translation.Project) ProjectInfo {
	translated := 0
	for _, para := range p.Target.Paragraphs {
		if para.Text != "" {
			translated++
		}
	}
	return ProjectInfo{
		Path:         projectPath,
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

func (a *App) getOrLoad(projectPath string) (*translation.Project, error) {
	// Defense-in-depth: reject paths that escape the projects directory even if
	// the handler layer already validated (guards against future handler gaps).
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

func (a *App) setupTranslatorLocked(projectPath string, p *translation.Project) {
	t, err := ai.NewTranslator(p)
	if err != nil {
		log.Printf("could not create translator for %s: %v", projectPath, err)
		return
	}
	broadcast := a.broadcast
	var saveCount int32
	t.OnTranslationComplete = func(index int, text string) {
		if broadcast != nil {
			broadcast(TranslationEvent{
				ProjectPath: projectPath,
				Index:       index,
				Text:        text,
			})
		}
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

// ─── Settings ────────────────────────────────────────────────────────────────

// maskAPIKey replaces all but the last 4 characters of an API key with '*'.
// This allows the frontend to confirm a key is set without exposing the full value.
// NOTE: identical copy exists in saatool-wails/app.go — keep in sync.
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	return strings.Repeat("*", len(key)-4) + key[len(key)-4:]
}

func (a *App) GetSettings() Settings {
	return Settings{
		// C-1: Return a masked key so the full value is never exposed in the
		// API response. SaveSettings ignores submitted values that contain '*'.
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
	// Validate projects directory so an invalid path is not persisted (avoids crash in ProjectsDir).
	if dir := strings.TrimSpace(s.ProjectsDirectory); dir != "" {
		dir = filepath.Clean(dir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("cannot use folder: %w", err)
		}
	}
	// C-1: Only update the API key when the submitted value is not the masked
	// placeholder (i.e. the user actually typed a new key). A value containing
	// '*' is treated as "keep existing".
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
	// new limit immediately. Each acquire site captures the channel in a local
	// variable before sending, so in-flight goroutines always release to the
	// same channel they acquired from; the old channel is then GC'd.
	if cap(a.translationSem) != s.MaxConcurrentTranslations {
		a.mu.Lock()
		a.translationSem = make(chan struct{}, s.MaxConcurrentTranslations)
		a.mu.Unlock()
	}
	return config.SaveOptions()
}

// ─── Projects ────────────────────────────────────────────────────────────────

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
		result = append(result, projectToInfo(f.Path, p))
	}
	return result, nil
}

func (a *App) LoadProject(projectPath string) (ProjectInfo, error) {
	if err := validateProjectPath(projectPath); err != nil {
		return ProjectInfo{}, fmt.Errorf("invalid project path: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	p, err := translation.LoadProject(projectPath)
	if err != nil {
		return ProjectInfo{}, fmt.Errorf("failed to load project: %w", err)
	}
	p.Normalize()
	a.projects[projectPath] = p
	a.setupTranslatorLocked(projectPath, p)

	return projectToInfo(projectPath, p), nil
}

func (a *App) ImportEPUB(epubPath, from, to string, direct bool) (ProjectInfo, error) {
	p, err := actions.ImportEPUBFile(epubPath, from, to, direct)
	if err != nil {
		return ProjectInfo{}, fmt.Errorf("failed to import EPUB: %w", err)
	}

	base := filepath.Base(epubPath)
	base = base[:len(base)-len(filepath.Ext(base))]
	p.SetName(base)

	savedPath, err := p.Save()
	if err != nil {
		return ProjectInfo{}, fmt.Errorf("EPUB imported but save failed: %w", err)
	}

	if data, mime := extractCoverFromEPUB(epubPath); len(data) > 0 {
		saveCoverForProject(savedPath, data, mime)
	}

	a.mu.Lock()
	a.projects[savedPath] = p
	a.setupTranslatorLocked(savedPath, p)
	a.mu.Unlock()

	return projectToInfo(savedPath, p), nil
}

func (a *App) ImportProjectFile(spzPath string) (ProjectInfo, error) {
	p, err := translation.LoadProject(spzPath)
	if err != nil {
		return ProjectInfo{}, fmt.Errorf("failed to read project file: %w", err)
	}
	p.Normalize()

	savedPath, err := p.Save()
	if err != nil {
		return ProjectInfo{}, fmt.Errorf("failed to save imported project: %w", err)
	}

	a.mu.Lock()
	a.projects[savedPath] = p
	a.setupTranslatorLocked(savedPath, p)
	a.mu.Unlock()

	return projectToInfo(savedPath, p), nil
}

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

// WriteProjectTo writes the project as a .spz file to the given writer.
// Used by the export endpoint to stream the file to the HTTP response.
func (a *App) WriteProjectTo(projectPath string, w io.Writer) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	_, err = p.SaveToWriter(w)
	return err
}

// WriteEPUBTo writes the project as an EPUB (translation + metadata) to w.
func (a *App) WriteEPUBTo(projectPath string, w io.Writer) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	return export.ProjectToEPUBWriter(p, w)
}

// WriteTranslationOnlyTo writes only the target paragraphs to w (plain text).
func (a *App) WriteTranslationOnlyTo(projectPath string, w io.Writer) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	for i, para := range p.Target.Paragraphs {
		if i > 0 && para.IsChapterStart {
			_, _ = io.WriteString(w, "\n---\n\n")
		}
		_, _ = io.WriteString(w, para.Text)
		_, _ = io.WriteString(w, "\n\n")
	}
	return nil
}

// ─── Paragraphs ──────────────────────────────────────────────────────────────

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
		srcPara, sErr := p.GetSourceParagraph(i)
		if sErr != nil {
			return nil, fmt.Errorf("failed to read source paragraph %d: %w", i, sErr)
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

// GetLastPosition returns the last saved reading position. If the saved position
// is 0 and there is translated content, returns the last translated paragraph and
// target view so the book opens at the end of the translated part.
func (a *App) GetLastPosition(projectPath string) (Position, error) {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return Position{}, err
	}
	index, sourceView := p.EffectiveOpenPosition()
	return Position{
		Index:      index,
		SourceView: sourceView,
	}, nil
}

func (a *App) SavePosition(projectPath string, index int, sourceView bool) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	// Only update the in-memory position — do NOT call p.Save() here.
	// Calling p.Save() on every page turn acquires p.mutex for a full
	// gzip+JSON write of the entire project, which blocks GetParagraphsBatch
	// (also needs p.mutex) and stalls all HTTP requests. Position is already
	// persisted by the auto-save in OnTranslationComplete (every 5 paragraphs)
	// and by the explicit Save on the Library screen.
	p.SetPosition(sourceView, index)
	return nil
}

// SetActiveProject sets the project that is allowed to run translation (e.g. the book open in Reader).
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
	// Block early: if the whole range is already translated, do not start any work.
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
		// 1. Current paragraph first
		sem := a.translationSem
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		if err := t.Translate(ctx, fromIndex); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("translation error (current %d): %v", fromIndex, err)
		}
		<-sem

		// 2. Paragraphs ahead in batches
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
			batchSem := a.translationSem
			select {
			case <-ctx.Done():
				return
			case batchSem <- struct{}{}:
			}
			go func(batch []int) {
				defer func() { <-batchSem }()
				if err := t.TranslateAndProofReadBatch(ctx, batch); err != nil && !errors.Is(err, context.Canceled) {
					log.Printf("batch translation error (%v): %v", batch, err)
				}
			}(indices)
		}
	}()
	return nil
}

// TranslateWholeBook translates the entire project from paragraph 0 to end in the background.
// It uses the same cancellable context as TranslateParagraphs; when SetActiveProject is called
// for another project (e.g. user opens a different book), this run is cancelled.
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
	// Start after the last translated paragraph, so front-matter gaps don't
	// reset progress to index 0.
	startIndex := p.LastTranslatedIndex() + 1
	if startIndex >= total {
		return nil
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
		for i := startIndex; i < total; i += batchSize {
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
	// with background translation, preventing unbounded goroutine accumulation
	// when the user taps Fix rapidly.
	// The semaphore send is inside a goroutine so the HTTP handler returns
	// immediately (same fix as TranslateParagraphs).
	fixSem := a.translationSem
	go func() {
		fixSem <- struct{}{}
		defer func() { <-fixSem }()
		if err := t.FixTranslation(a.ctx, index); err != nil {
			log.Printf("fix translation error at %d: %v", index, err)
		}
	}()
	return nil
}

// FixTranslationSync runs the fix in the current goroutine and returns when done.
// Used by the HTTP handler so the client receives 204 only after the fix completes.
// It uses fixSem (not translationSem) so Fix runs immediately without waiting behind batch translation.
func (a *App) FixTranslationSync(ctx context.Context, projectPath string, index int) error {
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
	a.fixSem <- struct{}{}
	defer func() { <-a.fixSem }()
	return t.FixTranslation(ctx, index)
}

// ─── Book details ─────────────────────────────────────────────────────────────

func (a *App) GetBookDetailsInfo(projectPath string) (BookDetailsInfo, error) {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return BookDetailsInfo{}, err
	}
	return BookDetailsInfo{
		Title:        p.Title,
		Author:       p.Author,
		Genre:        p.Genre,
		Synopsis:     p.Synopsis,
		WritingStyle: p.WritingStyle,
		Characters:   p.Characters,
	}, nil
}

func (a *App) FetchBookDetails(projectPath string) (BookDetailsInfo, error) {
	a.mu.Lock()
	t, tok := a.translators[projectPath]
	p, pok := a.projects[projectPath]
	a.mu.Unlock()

	if !tok || !pok {
		if _, err := a.LoadProject(projectPath); err != nil {
			return BookDetailsInfo{}, fmt.Errorf("could not load project: %w", err)
		}
		a.mu.Lock()
		t = a.translators[projectPath]
		p = a.projects[projectPath]
		a.mu.Unlock()
	}
	if t == nil {
		return BookDetailsInfo{}, fmt.Errorf("could not create translator — check API key in Settings")
	}
	if p == nil {
		return BookDetailsInfo{}, fmt.Errorf("project not loaded")
	}

	bookDetails, err := t.GetBookDetails(a.ctx)
	if err != nil {
		return BookDetailsInfo{}, fmt.Errorf("AI fetch failed: %w", err)
	}

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

	a.mu.Lock()
	a.setupTranslatorLocked(projectPath, p)
	a.mu.Unlock()

	return BookDetailsInfo{
		Title:        p.Title,
		Author:       p.Author,
		Genre:        p.Genre,
		Synopsis:     p.Synopsis,
		WritingStyle: p.WritingStyle,
		Characters:   p.Characters,
	}, nil
}

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

	a.mu.Lock()
	a.setupTranslatorLocked(projectPath, p)
	a.mu.Unlock()
	return nil
}

// ─── Glossary ────────────────────────────────────────────────────────────────

func (a *App) GetGlossary(projectPath string) (map[string]string, error) {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return nil, err
	}
	return p.GetGlossary(), nil
}

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

// ─── Log ─────────────────────────────────────────────────────────────────────

func (a *App) GetLog() []string {
	return GetMemLogger().GetLines()
}

// TestNotification sends a test error notification immediately, bypassing the cooldown.
func (a *App) TestNotification() {
	GetMemLogger().SendTestNotification()
}

// ─── Cover helpers ────────────────────────────────────────────────────────────

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

func extractCoverFromEPUB(epubPath string) ([]byte, string) {
	r, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, ""
	}
	defer r.Close()

	// findZip finds a zip entry by normalised path.
	// NOTE: identical closure exists in saatool-wails/app.go — keep in sync.
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
	const maxManifestItems = 10_000
	if len(pkg.Manifest.Items) > maxManifestItems {
		return nil, ""
	}

	var coverID string
	for _, m := range pkg.Metadata.Metas {
		if strings.EqualFold(m.Name, "cover") {
			coverID = m.Content
			break
		}
	}

	var coverHref, coverMime string
	for _, item := range pkg.Manifest.Items {
		if strings.Contains(item.Properties, "cover-image") &&
			strings.HasPrefix(item.MediaType, "image/") {
			coverHref = item.Href
			coverMime = item.MediaType
			break
		}
		if coverID != "" && item.ID == coverID &&
			strings.HasPrefix(item.MediaType, "image/") {
			coverHref = item.Href
			coverMime = item.MediaType
		}
	}
	if coverHref == "" {
		return nil, ""
	}

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
