package cli

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	termansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

type runMarkdownRenderFunc func(content string, width int) (string, error)

var trailingRunMarkdownSGR = regexp.MustCompile(`(?:\x1b\[[0-9;]*m)+$`)

// renderRunMarkdown renders untrusted assistant Markdown for an interactive
// terminal. Existing escape sequences are removed first so model output cannot
// inject terminal controls. The final hard-wrap is a guard for long code, URLs,
// and other tokens that Markdown renderers intentionally preserve.
func renderRunMarkdown(content string, width int) string {
	return renderRunMarkdownWith(content, width, renderGlamourRunMarkdown)
}

func renderRunMarkdownWith(content string, width int, render runMarkdownRenderFunc) string {
	if width < 12 {
		width = 12
	}
	content = strings.Trim(sanitizeRunOutput(content), "\r\n")
	if strings.TrimSpace(content) == "" {
		return ""
	}

	rendered, err := render(content, width)
	if err != nil {
		return renderPlainRunMarkdown(content, width)
	}
	rendered = strings.Trim(rendered, "\n")
	rendered = termansi.Hardwrap(rendered, width, true)
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		lines[i] = trimRunMarkdownLine(line)
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

func sanitizeRunOutput(content string) string {
	return termansi.Strip(content)
}

func trimRunMarkdownLine(line string) string {
	plain := termansi.Strip(line)
	plain = strings.TrimRightFunc(plain, unicode.IsSpace)
	if plain == "" {
		return ""
	}
	visibleWidth := termansi.StringWidth(plain)
	line = termansi.Truncate(line, visibleWidth, "")
	if strings.Contains(line, "\x1b[") {
		line = trailingRunMarkdownSGR.ReplaceAllString(line, "\x1b[0m")
	}
	return line
}

func renderGlamourRunMarkdown(content string, width int) (string, error) {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(runMarkdownStyle()),
		glamour.WithWordWrap(width),
		glamour.WithTableWrap(true),
		glamour.WithInlineTableLinks(true),
		glamour.WithColorProfile(termenv.ANSI256),
	)
	if err != nil {
		return "", err
	}
	return renderer.Render(content)
}

func renderPlainRunMarkdown(content string, width int) string {
	var lines []string
	for _, raw := range strings.Split(content, "\n") {
		wrapped := termansi.Hardwrap(raw, width, true)
		if wrapped == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, strings.Split(wrapped, "\n")...)
	}
	return strings.Join(lines, "\n")
}

func runMarkdownStyle() glamouransi.StyleConfig {
	style := glamourstyles.DarkStyleConfig
	zero := uint(0)
	one := uint(1)

	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	style.Document.Color = runMarkdownColor("252")
	style.Document.Margin = &zero
	style.Text.Color = runMarkdownColor("252")

	style.BlockQuote.Color = runMarkdownColor("244")
	style.BlockQuote.Indent = &one
	style.BlockQuote.IndentToken = runMarkdownString("│ ")

	style.Heading.Color = runMarkdownColor("179")
	style.Heading.Bold = runMarkdownBool(true)
	for _, heading := range []*glamouransi.StyleBlock{
		&style.H1,
		&style.H2,
		&style.H3,
		&style.H4,
		&style.H5,
		&style.H6,
	} {
		heading.Prefix = ""
		heading.Suffix = ""
		heading.BackgroundColor = nil
	}
	style.H1.Color = runMarkdownColor("179")
	style.H2.Color = runMarkdownColor("179")
	style.H3.Color = runMarkdownColor("252")
	style.H4.Color = runMarkdownColor("250")
	style.H5.Color = runMarkdownColor("250")
	style.H6.Color = runMarkdownColor("244")

	style.HorizontalRule.Color = runMarkdownColor("240")
	style.HorizontalRule.Format = "\n────────\n"
	style.Item.BlockPrefix = "• "
	style.Task.Ticked = "[✓] "
	style.Task.Unticked = "[ ] "

	style.Link.Color = runMarkdownColor("179")
	style.Link.Underline = runMarkdownBool(true)
	style.LinkText.Color = runMarkdownColor("179")
	style.LinkText.Bold = runMarkdownBool(true)
	style.Image.Color = runMarkdownColor("179")
	style.ImageText.Color = runMarkdownColor("244")

	style.Code.Color = runMarkdownColor("179")
	style.Code.BackgroundColor = runMarkdownColor("237")
	style.CodeBlock.Color = runMarkdownColor("252")
	style.CodeBlock.BackgroundColor = runMarkdownColor("235")
	style.CodeBlock.Margin = &zero
	style.CodeBlock.Theme = ""
	style.CodeBlock.Chroma = nil

	style.Table.Color = runMarkdownColor("250")
	style.Table.Margin = &zero
	style.DefinitionTerm.Color = runMarkdownColor("179")
	style.DefinitionTerm.Bold = runMarkdownBool(true)
	style.DefinitionDescription.BlockPrefix = "\n· "

	return style
}

func runMarkdownColor(value string) *string  { return &value }
func runMarkdownString(value string) *string { return &value }
func runMarkdownBool(value bool) *bool       { return &value }
