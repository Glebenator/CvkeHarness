package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	termansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

type markdownRenderFunc func(content string, width int) (string, error)

// renderMarkdown renders untrusted assistant output with a compact terminal
// stylesheet. Any pre-existing terminal escape sequences are removed before
// parsing so the content cannot inject styles or terminal controls.
func renderMarkdown(content string, width int) string {
	return renderMarkdownWith(content, width, renderGlamourMarkdown)
}

func renderMarkdownWith(content string, width int, render markdownRenderFunc) string {
	width = maxInt(width, 12)
	content = termansi.Strip(content)
	if strings.TrimSpace(content) == "" {
		return ""
	}
	content = strings.Trim(content, "\r\n")

	rendered, err := render(content, width)
	if err != nil {
		return renderPlainMarkdown(content, width)
	}

	// Glamour wraps prose and tables itself. Hardwrap is a final width guard for
	// long code lines, URLs, and other unbroken tokens that terminal Markdown
	// renderers intentionally preserve.
	rendered = strings.Trim(rendered, "\n")
	return strings.Trim(termansi.Hardwrap(rendered, width, true), "\n")
}

func renderGlamourMarkdown(content string, width int) (string, error) {
	return renderGlamourMarkdownWithProfile(content, width, lipgloss.ColorProfile())
}

func renderGlamourMarkdownWithProfile(content string, width int, profile termenv.Profile) (string, error) {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStyles(cvkeMarkdownStyle()),
		glamour.WithWordWrap(width),
		glamour.WithTableWrap(true),
		glamour.WithInlineTableLinks(true),
		glamour.WithColorProfile(profile),
	)
	if err != nil {
		return "", err
	}
	return renderer.Render(content)
}

func renderPlainMarkdown(content string, width int) string {
	var lines []string
	for _, raw := range strings.Split(content, "\n") {
		wrapped := termansi.Hardwrap(raw, width, true)
		if wrapped == "" {
			lines = append(lines, "")
			continue
		}
		for _, line := range strings.Split(wrapped, "\n") {
			lines = append(lines, styleBright.Render(line))
		}
	}
	return strings.Join(lines, "\n")
}

func cvkeMarkdownStyle() glamouransi.StyleConfig {
	style := glamourstyles.DarkStyleConfig
	zero := uint(0)
	one := uint(1)

	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	style.Document.Color = nil
	style.Document.Margin = &zero
	style.Text.Color = markdownColor(colorBrightText)

	style.BlockQuote.Color = markdownColor(colorMuted)
	style.BlockQuote.Indent = &one
	style.BlockQuote.IndentToken = markdownString("│ ")

	style.Heading.Color = markdownColor(colorAccent)
	style.Heading.Bold = markdownBool(true)
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
	style.H1.Color = markdownColor(colorAccent)
	style.H2.Color = markdownColor(colorAccent)
	style.H3.Color = markdownColor(colorBrightText)
	style.H4.Color = markdownColor(colorBase)
	style.H5.Color = markdownColor(colorBase)
	style.H6.Color = markdownColor(colorMuted)

	style.HorizontalRule.Color = markdownColor(colorSubtle)
	style.HorizontalRule.Format = "\n────────\n"
	style.Item.BlockPrefix = "• "
	style.Task.Ticked = "[✓] "
	style.Task.Unticked = "[ ] "

	style.Link.Color = markdownColor(colorAccent)
	style.Link.Underline = markdownBool(true)
	style.LinkText.Color = markdownColor(colorAccent)
	style.LinkText.Bold = markdownBool(true)
	style.Image.Color = markdownColor(colorAccent)
	style.ImageText.Color = markdownColor(colorMuted)

	style.Code.Color = markdownColor(colorAccent)
	style.Code.BackgroundColor = markdownColor(colorHighlight)
	style.CodeBlock.Color = markdownColor(colorBrightText)
	style.CodeBlock.BackgroundColor = markdownColor(colorSurface)
	style.CodeBlock.Margin = &zero
	style.CodeBlock.Theme = ""
	style.CodeBlock.Chroma = nil

	style.Table.Color = markdownColor(colorBase)
	style.DefinitionTerm.Color = markdownColor(colorAccent)
	style.DefinitionTerm.Bold = markdownBool(true)
	style.DefinitionDescription.BlockPrefix = "\n· "

	return style
}

func markdownColor(color lipgloss.Color) *string {
	value := string(color)
	return &value
}

func markdownString(value string) *string { return &value }
func markdownBool(value bool) *bool       { return &value }
