package agent

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

func ChunkMarkdown(content, source string) []*schema.Document {
	lines := strings.Split(content, "\n")

	var docs []*schema.Document
	var current strings.Builder
	var title string

	flush := func() {
		text := strings.TrimSpace(current.String())
		current.Reset()
		if text == "" {
			return
		}

		if title != "" {
			text = title + "\n" + text
		}

		doc := &schema.Document{
			ID:       fmt.Sprintf("%s#%d", source, len(docs)),
			Content:  text,
			MetaData: map[string]any{"source": source},
		}
		docs = append(docs, doc)
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "## "):
			flush()
			title = trimmed
		case strings.HasPrefix(trimmed, "# "):
			// Skip the document-level title.
		default:
			current.WriteString(line)
			current.WriteString("\n")
		}
	}
	flush()

	return docs
}

func FormatDocs(docs []*schema.Document) string {
	var b strings.Builder
	for i, d := range docs {
		source, _ := d.MetaData["source"].(string)
		fmt.Fprintf(&b, "[Fragment %d, source: %s]\n%s\n\n", i+1, source, d.Content)
	}
	return strings.TrimSpace(b.String())
}
