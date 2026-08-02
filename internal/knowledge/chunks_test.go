package knowledge

import "testing"

func TestSplitMarkdownPreservesCourseSectionsForTargetedRetrieval(t *testing.T) {
	chunks := SplitMarkdown("Introduction\n\n## Temperature\nCotton reaches 98 C.\n\n## Checks\nStop when heat is low.")
	if len(chunks) != 3 {
		t.Fatalf("chunks = %#v", chunks)
	}
	if chunks[1].Heading != "Temperature" || chunks[1].Body != "Cotton reaches 98 C." {
		t.Fatalf("temperature chunk = %#v", chunks[1])
	}
}

func TestSplitMarkdownDoesNotTreatCodeCommentsAsCourseHeadings(t *testing.T) {
	chunks := SplitMarkdown("## Example\n```sh\n# not a heading\necho ok\n```")
	if len(chunks) != 1 || chunks[0].Heading != "Example" {
		t.Fatalf("chunks = %#v", chunks)
	}
}
