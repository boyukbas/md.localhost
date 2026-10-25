package server

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// md is the shared, concurrency-safe Markdown converter.
var md = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,            // tables, strikethrough, task lists, autolinks
		extension.Footnote,       // [^1] style footnotes
		extension.DefinitionList, // term / : definition
		extension.Typographer,    // smart quotes and dashes
		emoji.Emoji,              // :sparkles:
	),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(), // stable #anchors for the table of contents
		parser.WithAttribute(),     // {#id .class} on headings
	),
	goldmark.WithRendererOptions(
		// These are local files the user chose to open, so inline HTML in
		// them is rendered rather than escaped. See SECURITY notes in README.
		html.WithUnsafe(),
	),
)

// frontMatterRe matches a leading YAML front matter block.
var frontMatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n`)

// titleRe pulls a title out of front matter without a full YAML parser.
var titleRe = regexp.MustCompile(`(?mi)^title:\s*["']?(.+?)["']?\s*$`)

// h1Re finds the first ATX level-1 heading in the body.
var h1Re = regexp.MustCompile(`(?m)^#\s+(.+?)\s*#*\s*$`)

type document struct {
	HTML        string
	Title       string
	FrontMatter string
}

// renderMarkdown converts source Markdown into the HTML fragment shown in the
// viewer, along with a best-effort document title.
func renderMarkdown(src []byte) (document, error) {
	var doc document

	body := src
	if m := frontMatterRe.FindSubmatch(src); m != nil {
		doc.FrontMatter = strings.TrimSpace(string(m[1]))
		body = src[len(m[0]):]
		if t := titleRe.FindStringSubmatch(doc.FrontMatter); t != nil {
			doc.Title = strings.TrimSpace(t[1])
		}
	}

	if doc.Title == "" {
		if m := h1Re.FindSubmatch(body); m != nil {
			doc.Title = strings.TrimSpace(stripInlineMarkdown(string(m[1])))
		}
	}

	var buf bytes.Buffer
	if err := md.Convert(body, &buf); err != nil {
		return doc, err
	}
	doc.HTML = buf.String()
	return doc, nil
}

var inlineMarkupRe = regexp.MustCompile("[`*_]")

// stripInlineMarkdown removes the most common inline markers so a heading is
// usable as a browser tab title.
func stripInlineMarkdown(s string) string {
	return strings.TrimSpace(inlineMarkupRe.ReplaceAllString(s, ""))
}
