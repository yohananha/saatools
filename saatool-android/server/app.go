package server

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
}

// newApp creates a new App instance.
func newApp(broadcast BroadcastFn) *App {
	return &App{
		ctx:            context.Background(),
		projects:       make(map[string]*translation.Project),
		translators:    make(map[string]*ai.Translator),
		broadcast:      broadcast,
		translationSem: make(chan struct{}, maxConcurrentTranslations),
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
	t.OnTranslationComplete = func(index int, text string) {
		if broadcast != nil {
			broadcast(TranslationEvent{
				ProjectPath: projectPath,
				Index:       index,
				Text:        text,
			})
		}
	}
	a.translators[projectPath] = t
}

// ─── Settings ────────────────────────────────────────────────────────────────

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

func (a *App) ImportEPUB(epubPath, from, to string) (ProjectInfo, error) {
	p, err := actions.ImportEPUBFile(epubPath, from, to)
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

func (a *App) GetLastPosition(projectPath string) (Position, error) {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return Position{}, err
	}
	return Position{
		Index:      p.LastParagraphIndex,
		SourceView: p.LastSourceView,
	}, nil
}

func (a *App) SavePosition(projectPath string, index int, sourceView bool) error {
	p, err := a.getOrLoad(projectPath)
	if err != nil {
		return err
	}
	p.SetPosition(sourceView, index)
	_, err = p.Save()
	return err
}

const maxConcurrentTranslations = 2
const translationBatchSize = 5

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

	for i := fromIndex; i < end; i += translationBatchSize {
		batchEnd := i + translationBatchSize
		if batchEnd > end {
			batchEnd = end
		}
		indices := make([]int, batchEnd-i)
		for j := range indices {
			indices[j] = i + j
		}
		// Block until a slot is free in the shared semaphore. Because the
		// semaphore lives on the App (not per-call), concurrent invocations
		// of TranslateParagraphs all compete for the same pool of slots,
		// capping total concurrent API calls regardless of call frequency.
		a.translationSem <- struct{}{}
		go func(batch []int) {
			defer func() { <-a.translationSem }()
			if err := t.TranslateAndProofReadBatch(a.ctx, batch); err != nil {
				log.Printf("batch translation error (%v): %v", batch, err)
			}
		}(indices)
	}
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
	go func() {
		if err := t.FixTranslation(a.ctx, index); err != nil {
			log.Printf("fix translation error at %d: %v", index, err)
		}
	}()
	return nil
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
