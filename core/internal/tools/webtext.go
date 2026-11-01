// webtext.go turns an HTML page into compact Markdown (or plain text)
// for the model: the main content when the page marks it (<main>,
// <article>, role=main), headings, paragraphs, lists, links, code,
// quotes and simple tables, without scripts, styles or page chrome.

package tools

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

type webPage struct {
	Title string
	Body  string
}

// skipTags never carry readable content.
var skipTags = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true, atom.Svg: true,
	atom.Iframe: true, atom.Object: true, atom.Embed: true, atom.Canvas: true, atom.Head: true,
	atom.Button: true, atom.Select: true, atom.Input: true, atom.Textarea: true, atom.Form: true,
}

// chromeTags are page furniture dropped when the page has a main part.
var chromeTags = map[atom.Atom]bool{atom.Nav: true, atom.Header: true, atom.Footer: true, atom.Aside: true}

var (
	blankRuns = regexp.MustCompile(`\n{3,}`)
	spaceRuns = regexp.MustCompile(`[ \t\r\f\v]+`)
)

func htmlToMarkdown(src string, base *url.URL, plain bool) webPage {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return webPage{Body: src}
	}
	var page webPage
	if t := findFirst(doc, func(n *html.Node) bool { return n.DataAtom == atom.Title }); t != nil {
		page.Title = strings.TrimSpace(textOf(t))
	}
	root := findFirst(doc, func(n *html.Node) bool {
		return n.DataAtom == atom.Main || n.DataAtom == atom.Article || attr(n, "role") == "main"
	})
	dropChrome := root != nil
	if root == nil {
		root = findFirst(doc, func(n *html.Node) bool { return n.DataAtom == atom.Body })
	}
	if root == nil {
		root = doc
	}
	w := &mdWriter{base: base, plain: plain, dropChrome: dropChrome}
	w.node(root)
	body := blankRuns.ReplaceAllString(w.b.String(), "\n\n")
	page.Body = strings.TrimSpace(body)
	return page
}

func findFirst(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n.Type == html.ElementNode && match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findFirst(c, match); f != nil {
			return f
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && skipTags[n.DataAtom] && n.DataAtom != atom.Head {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

type mdWriter struct {
	b          strings.Builder
	base       *url.URL
	plain      bool
	dropChrome bool
	pre        int
	listDepth  int
}

// block starts a new paragraph-level element.
func (w *mdWriter) block() {
	s := w.b.String()
	switch {
	case s == "" || strings.HasSuffix(s, "\n\n"):
	case strings.HasSuffix(s, "\n"):
		w.b.WriteString("\n")
	default:
		w.b.WriteString("\n\n")
	}
}

func (w *mdWriter) line() {
	if s := w.b.String(); s != "" && !strings.HasSuffix(s, "\n") {
		w.b.WriteString("\n")
	}
}

func (w *mdWriter) text(s string) {
	if w.pre > 0 {
		w.b.WriteString(s)
		return
	}
	s = spaceRuns.ReplaceAllString(strings.ReplaceAll(s, "\n", " "), " ")
	cur := w.b.String()
	if strings.HasPrefix(s, " ") && (cur == "" || strings.HasSuffix(cur, " ") || strings.HasSuffix(cur, "\n")) {
		s = strings.TrimLeft(s, " ")
	}
	w.b.WriteString(s)
}

func (w *mdWriter) children(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.node(c)
	}
}

func (w *mdWriter) node(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.text(n.Data)
		return
	case html.DocumentNode:
		w.children(n)
		return
	case html.ElementNode:
	default:
		return
	}
	if skipTags[n.DataAtom] || (w.dropChrome && chromeTags[n.DataAtom]) || attr(n, "hidden") != "" ||
		attr(n, "aria-hidden") == "true" {
		return
	}
	md := !w.plain
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		w.block()
		if md {
			w.b.WriteString(strings.Repeat("#", int(n.Data[1]-'0')) + " ")
		}
		w.b.WriteString(strings.TrimSpace(spaceRuns.ReplaceAllString(textOf(n), " ")))
		w.block()
	case atom.P, atom.Div, atom.Section, atom.Article, atom.Main, atom.Header, atom.Footer, atom.Nav,
		atom.Aside, atom.Figure, atom.Dl, atom.Details, atom.Summary:
		w.block()
		w.children(n)
		w.block()
	case atom.Br:
		w.b.WriteString("\n")
	case atom.Hr:
		w.block()
		if md {
			w.b.WriteString("---")
		}
		w.block()
	case atom.Pre:
		w.block()
		if md {
			w.b.WriteString("```\n")
		}
		w.pre++
		w.b.WriteString(strings.TrimRight(textOf(n), "\n"))
		w.pre--
		if md {
			w.b.WriteString("\n```")
		}
		w.block()
	case atom.Code:
		if w.pre > 0 || !md {
			w.children(n)
			return
		}
		w.b.WriteString("`" + strings.TrimSpace(textOf(n)) + "`")
	case atom.Strong, atom.B:
		w.wrap(n, "**", md)
	case atom.Em, atom.I:
		w.wrap(n, "_", md)
	case atom.A:
		label := strings.TrimSpace(spaceRuns.ReplaceAllString(textOf(n), " "))
		href := w.resolve(attr(n, "href"))
		if label == "" {
			return
		}
		if md && href != "" && !strings.HasPrefix(href, "javascript:") {
			w.text("[" + label + "](" + href + ")")
		} else {
			w.text(label)
		}
	case atom.Img:
		if alt := strings.TrimSpace(attr(n, "alt")); alt != "" {
			if md {
				w.text("![" + alt + "]")
			} else {
				w.text(alt)
			}
		}
	case atom.Ul, atom.Ol:
		w.block()
		w.listDepth++
		i := 0
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || c.DataAtom != atom.Li {
				continue
			}
			i++
			w.line()
			w.b.WriteString(strings.Repeat("  ", w.listDepth-1))
			if n.DataAtom == atom.Ol {
				w.b.WriteString(strconv.Itoa(i) + ". ")
			} else {
				w.b.WriteString("- ")
			}
			w.children(c)
		}
		w.listDepth--
		w.block()
	case atom.Blockquote:
		w.block()
		inner := &mdWriter{base: w.base, plain: w.plain, dropChrome: w.dropChrome}
		inner.children(n)
		for _, l := range strings.Split(strings.TrimSpace(inner.b.String()), "\n") {
			if md {
				w.b.WriteString("> ")
			}
			w.b.WriteString(l + "\n")
		}
		w.block()
	case atom.Table:
		w.table(n)
	case atom.Dt:
		w.line()
		w.wrap(n, "**", md)
	case atom.Dd:
		w.line()
		w.b.WriteString("  ")
		w.children(n)
	default:
		w.children(n)
	}
}

func (w *mdWriter) wrap(n *html.Node, mark string, md bool) {
	inner := strings.TrimSpace(spaceRuns.ReplaceAllString(textOf(n), " "))
	if inner == "" {
		return
	}
	if !md {
		w.text(inner)
		return
	}
	w.text(mark + inner + mark)
}

func (w *mdWriter) resolve(href string) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if w.base != nil {
		ref = w.base.ResolveReference(ref)
	}
	return ref.String()
}

// table renders rows as Markdown pipes (plain text: tab-separated).
func (w *mdWriter) table(n *html.Node) {
	var rows [][]string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.DataAtom == atom.Tr {
			var row []string
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.ElementNode && (c.DataAtom == atom.Td || c.DataAtom == atom.Th) {
					cell := strings.TrimSpace(spaceRuns.ReplaceAllString(strings.ReplaceAll(textOf(c), "\n", " "), " "))
					row = append(row, strings.ReplaceAll(cell, "|", "\\|"))
				}
			}
			if len(row) > 0 {
				rows = append(rows, row)
			}
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	if len(rows) == 0 {
		return
	}
	w.block()
	if w.plain {
		for _, r := range rows {
			w.b.WriteString(strings.Join(r, "\t") + "\n")
		}
		w.block()
		return
	}
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	for i, r := range rows {
		for len(r) < cols {
			r = append(r, "")
		}
		w.b.WriteString("| " + strings.Join(r, " | ") + " |\n")
		if i == 0 {
			w.b.WriteString("|" + strings.Repeat(" --- |", cols) + "\n")
		}
	}
	w.block()
}
