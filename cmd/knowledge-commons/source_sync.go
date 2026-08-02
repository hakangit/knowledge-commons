package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/hakangit/knowledge-commons/internal/knowledge"
)

type languageRoots map[string]string

func (roots languageRoots) String() string { return fmt.Sprint(map[string]string(roots)) }

func (roots languageRoots) Set(value string) error {
	language, root, ok := strings.Cut(value, "=")
	language = strings.TrimSpace(language)
	root = strings.Trim(strings.TrimSpace(root), "/")
	if !ok || language == "" || root == "" {
		return errors.New("language-root must use language=path")
	}
	roots[language] = root
	return nil
}

func syncSource(args []string) error {
	flags := flag.NewFlagSet("source sync", flag.ContinueOnError)
	directory := flags.String("directory", "", "repository working tree")
	sourceKey := flags.String("source", "", "stable source key")
	keyPrefix := flags.String("key-prefix", "", "knowledge key prefix")
	visibility := flags.String("visibility", knowledge.VisibilityShared, "shared or restricted")
	revision := flags.String("revision", "", "immutable source revision")
	repositoryURL := flags.String("repository-url", "", "web URL for immutable source links")
	baseURL := flags.String("url", os.Getenv("KNOWLEDGE_COMMONS_URL"), "Knowledge Commons URL")
	token := flags.String("token", os.Getenv("KNOWLEDGE_COMMONS_TOKEN"), "bearer token")
	subject := flags.String("subject", os.Getenv("KNOWLEDGE_COMMONS_SUBJECT"), "acts-for subject")
	actor := flags.String("actor", "", "header identity actor for local testing")
	roots := languageRoots{}
	flags.Var(roots, "language-root", "language=path relative to the repository; repeat per language")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *directory == "" || *sourceKey == "" || *keyPrefix == "" || *revision == "" || *repositoryURL == "" || *baseURL == "" || (*token == "" && *actor == "") || len(roots) == 0 {
		return errors.New("directory, source, key-prefix, revision, repository-url, url, authentication, and at least one language-root are required")
	}

	type candidate struct {
		language, path, relative string
	}
	var files []candidate
	for language, root := range roots {
		absoluteRoot := filepath.Join(*directory, filepath.FromSlash(root))
		err := filepath.WalkDir(absoluteRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				return nil
			}
			relative, err := filepath.Rel(absoluteRoot, path)
			if err != nil {
				return err
			}
			files = append(files, candidate{language: language, path: path, relative: filepath.ToSlash(relative)})
			return nil
		})
		if err != nil {
			return fmt.Errorf("walk %s: %w", absoluteRoot, err)
		}
		if info, err := os.Stat(absoluteRoot + ".md"); err == nil && !info.IsDir() {
			files = append(files, candidate{
				language: language,
				path:     absoluteRoot + ".md",
				relative: "_index.md",
			})
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].language == files[j].language {
			return files[i].relative < files[j].relative
		}
		return files[i].language < files[j].language
	})

	client := &http.Client{Timeout: 30 * time.Second}
	changed := 0
	for _, file := range files {
		raw, err := os.ReadFile(file.path)
		if err != nil {
			return err
		}
		title, body := parseMarkdown(string(raw), file.relative)
		sourcePath, err := filepath.Rel(*directory, file.path)
		if err != nil {
			return err
		}
		sourcePath = filepath.ToSlash(sourcePath)
		draft := knowledge.SourceDraft{
			KnowledgeKey:      normalizeKnowledgeKey(*keyPrefix + "/" + documentSlug(file.relative)),
			CanonicalLanguage: "en", Language: file.language, Title: title, Body: body,
			Visibility: *visibility, SourceKey: *sourceKey, SourcePath: sourcePath,
			SourceRevision: *revision,
			SourceURL:      strings.TrimRight(*repositoryURL, "/") + "/src/commit/" + *revision + "/" + sourcePath,
		}
		result, err := putSource(client, strings.TrimRight(*baseURL, "/"), *token, *subject, *actor, draft)
		if err != nil {
			return fmt.Errorf("sync %s: %w", sourcePath, err)
		}
		if result.Changed {
			changed++
		}
	}
	fmt.Printf("synced %d documents from %s (%d changed)\n", len(files), *sourceKey, changed)
	return nil
}

func putSource(client *http.Client, baseURL, token, subject, actor string, draft knowledge.SourceDraft) (knowledge.SourceResult, error) {
	payload, err := json.Marshal(draft)
	if err != nil {
		return knowledge.SourceResult{}, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		request, err := http.NewRequest(http.MethodPut, baseURL+"/v1/source-documents", bytes.NewReader(payload))
		if err != nil {
			return knowledge.SourceResult{}, err
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		request.Header.Set("Content-Type", "application/json")
		if subject != "" {
			request.Header.Set("X-Acts-For", subject)
		}
		if actor != "" {
			request.Header.Set("X-KC-Actor", actor)
		}
		response, err := client.Do(request)
		if err != nil {
			if attempt < 4 {
				time.Sleep(time.Duration(1<<attempt) * 250 * time.Millisecond)
				continue
			}
			return knowledge.SourceResult{}, err
		}
		contents, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if readErr != nil {
			return knowledge.SourceResult{}, readErr
		}
		if response.StatusCode == http.StatusOK {
			var result knowledge.SourceResult
			if err := json.Unmarshal(contents, &result); err != nil {
				return knowledge.SourceResult{}, err
			}
			return result, nil
		}
		if attempt < 4 && (response.StatusCode == http.StatusBadGateway || response.StatusCode == http.StatusServiceUnavailable || response.StatusCode == http.StatusGatewayTimeout) {
			time.Sleep(time.Duration(1<<attempt) * 250 * time.Millisecond)
			continue
		}
		return knowledge.SourceResult{}, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(contents)))
	}
	return knowledge.SourceResult{}, errors.New("source sync exhausted retries")
}

func parseMarkdown(raw, fallback string) (string, string) {
	body := raw
	title := ""
	if strings.HasPrefix(raw, "---\n") {
		if end := strings.Index(raw[4:], "\n---"); end >= 0 {
			frontmatter := raw[4 : 4+end]
			body = strings.TrimSpace(raw[4+end+4:])
			for _, line := range strings.Split(frontmatter, "\n") {
				key, value, ok := strings.Cut(line, ":")
				if ok && strings.TrimSpace(key) == "title" {
					title = strings.Trim(strings.TrimSpace(value), "\"'")
					break
				}
			}
		}
	}
	if title == "" {
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "# ") {
				title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
				break
			}
		}
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(fallback), filepath.Ext(fallback))
	}
	return title, strings.TrimSpace(body)
}

func documentSlug(relative string) string {
	slug := strings.TrimSuffix(filepath.ToSlash(relative), filepath.Ext(relative))
	if slug == "_index" {
		return "index"
	}
	return strings.TrimSuffix(slug, "/_index")
}

var repeatedDash = regexp.MustCompile(`-+`)

func normalizeKnowledgeKey(value string) string {
	var normalized strings.Builder
	for _, character := range strings.ToLower(filepath.ToSlash(value)) {
		switch {
		case unicode.IsLetter(character), unicode.IsDigit(character), strings.ContainsRune("/._-", character):
			normalized.WriteRune(character)
		default:
			normalized.WriteByte('-')
		}
	}
	parts := strings.Split(normalized.String(), "/")
	for index := range parts {
		parts[index] = strings.Trim(repeatedDash.ReplaceAllString(parts[index], "-"), "-.")
	}
	return strings.Trim(strings.Join(parts, "/"), "/")
}
