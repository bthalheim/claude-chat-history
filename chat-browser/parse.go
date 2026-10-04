package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Kinds of transcript entries.
const (
	kindUser       = "user"
	kindAssistant  = "assistant"
	kindThinking   = "thinking"
	kindToolUse    = "tool_use"
	kindToolResult = "tool_result"
)

type entry struct {
	kind string
	text string
	ts   time.Time
}

// item is one browsable file: a .jsonl session transcript or a .md daily log.
type item struct {
	path   string
	id     string // session id (jsonl only)
	isMD   bool
	title  string
	ts     time.Time
	count  int    // messages (jsonl) or log entries (md)
	search string // lowercased searchable text
}

type rawRec struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Timestamp   string `json:"timestamp"`
	Message     *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
	Content  json.RawMessage `json:"content"`
}

var (
	reminderRe = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	noisePrefs = []string{"<local-command", "<command-name>", "<command-message>", "<bash-"}
)

func scanLines(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			fn(line)
		}
		if err != nil {
			return nil
		}
	}
}

// parseSession converts a transcript into displayable entries.
func parseSession(path string) ([]entry, error) {
	var out []entry
	err := scanLines(path, func(line []byte) {
		var r rawRec
		if json.Unmarshal(line, &r) != nil || r.Message == nil || r.IsSidechain || r.IsMeta {
			return
		}
		if r.Type != "user" && r.Type != "assistant" {
			return
		}
		ts, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
		var s string
		if json.Unmarshal(r.Message.Content, &s) == nil {
			if r.Type == "user" {
				if t := cleanUser(s); t != "" {
					out = append(out, entry{kindUser, t, ts})
				}
			} else if t := strings.TrimSpace(s); t != "" {
				out = append(out, entry{kindAssistant, t, ts})
			}
			return
		}
		var blocks []block
		if json.Unmarshal(r.Message.Content, &blocks) != nil {
			return
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if r.Type == "user" {
					if t := cleanUser(b.Text); t != "" {
						out = append(out, entry{kindUser, t, ts})
					}
				} else if t := strings.TrimSpace(b.Text); t != "" {
					out = append(out, entry{kindAssistant, t, ts})
				}
			case "thinking":
				if t := strings.TrimSpace(b.Thinking); t != "" {
					out = append(out, entry{kindThinking, t, ts})
				}
			case "tool_use":
				out = append(out, entry{kindToolUse, fmt.Sprintf("%s %s", b.Name, compact(b.Input)), ts})
			case "tool_result":
				out = append(out, entry{kindToolResult, resultText(b.Content), ts})
			}
		}
	})
	return out, err
}

func cleanUser(s string) string {
	s = strings.TrimSpace(reminderRe.ReplaceAllString(s, ""))
	for _, p := range noisePrefs {
		if strings.HasPrefix(s, p) {
			return ""
		}
	}
	return s
}

func compact(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		v := strings.Join(strings.Fields(fmt.Sprint(m[k])), " ")
		if len(v) > 120 {
			v = v[:120] + "…"
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var bs []block
	if json.Unmarshal(raw, &bs) == nil {
		var sb strings.Builder
		for _, b := range bs {
			sb.WriteString(b.Text)
		}
		return strings.TrimSpace(sb.String())
	}
	return ""
}

// loadItems scans dir for session transcripts and daily markdown logs.
func loadItems(dir string) ([]item, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var items []item
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		p := filepath.Join(dir, f.Name())
		info, err := f.Info()
		if err != nil {
			continue
		}
		switch strings.ToLower(filepath.Ext(f.Name())) {
		case ".jsonl":
			entries, _ := parseSession(p)
			it := item{path: p, id: strings.TrimSuffix(f.Name(), filepath.Ext(f.Name())), ts: info.ModTime(), title: "(no prompts)"}
			var sb strings.Builder
			first := true
			for _, e := range entries {
				if e.kind == kindUser || e.kind == kindAssistant {
					it.count++
					sb.WriteString(strings.ToLower(e.text))
					sb.WriteByte('\n')
				}
				if first && !e.ts.IsZero() {
					it.ts, first = e.ts, false
				}
				if e.kind == kindUser && it.title == "(no prompts)" {
					it.title = oneLine(e.text)
				}
			}
			it.search = strings.ToLower(it.title) + "\n" + sb.String()
			items = append(items, it)
		case ".md":
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			txt := string(b)
			base := strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
			it := item{path: p, isMD: true, ts: info.ModTime(), search: strings.ToLower(txt),
				title: "Daily log " + base}
			if t, err := time.ParseInLocation("2006-01-02", base, time.Local); err == nil {
				it.ts = t
			}
			for _, l := range strings.Split(txt, "\n") {
				if strings.HasPrefix(l, "## ") {
					it.count++
				}
			}
			items = append(items, it)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ts.After(items[j].ts) })
	return items, nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
