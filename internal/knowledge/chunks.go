package knowledge

import "strings"

type Chunk struct {
	Ordinal int
	Heading string
	Body    string
}

func SplitMarkdown(body string) []Chunk {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	chunks := make([]Chunk, 0)
	heading := ""
	section := make([]string, 0)
	inFence := false
	flush := func() {
		content := strings.TrimSpace(strings.Join(section, "\n"))
		if content == "" && heading == "" {
			return
		}
		chunks = append(chunks, Chunk{Ordinal: len(chunks), Heading: heading, Body: content})
		section = section[:0]
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			section = append(section, line)
			continue
		}
		if !inFence {
			marker := 0
			for marker < len(trimmed) && trimmed[marker] == '#' {
				marker++
			}
			if marker > 0 && marker <= 6 && marker < len(trimmed) && trimmed[marker] == ' ' {
				flush()
				heading = strings.TrimSpace(trimmed[marker+1:])
				continue
			}
		}
		section = append(section, line)
	}
	flush()
	if len(chunks) == 0 {
		chunks = append(chunks, Chunk{Body: strings.TrimSpace(body)})
	}
	return chunks
}
