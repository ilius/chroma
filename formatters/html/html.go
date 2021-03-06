package html

import (
	"fmt"
	"html"
	"io"
	"sort"
	"strings"

	"github.com/alecthomas/chroma"
)

// Option sets an option of the HTML formatter.
type Option func(f *Formatter)

// Standalone configures the HTML formatter for generating a standalone HTML document.
func Standalone() Option { return func(f *Formatter) { f.standalone = true } }

// ClassPrefix sets the CSS class prefix.
func ClassPrefix(prefix string) Option { return func(f *Formatter) { f.prefix = prefix } }

// WithClasses emits HTML using CSS classes, rather than inline styles.
func WithClasses() Option { return func(f *Formatter) { f.classes = true } }

// TabWidth sets the number of characters for a tab. Defaults to 8.
func TabWidth(width int) Option { return func(f *Formatter) { f.tabWidth = width } }

// WithLineNumbers formats output with line numbers.
func WithLineNumbers() Option {
	return func(f *Formatter) {
		f.lineNumbers = true
	}
}

// HighlightLines higlights the given line ranges with the Highlight style.
//
// A range is the beginning and ending of a range as 1-based line numbers, inclusive.
func HighlightLines(ranges [][2]int) Option {
	return func(f *Formatter) {
		f.highlightRanges = ranges
		sort.Sort(f.highlightRanges)
	}
}

// New HTML formatter.
func New(options ...Option) *Formatter {
	f := &Formatter{}
	for _, option := range options {
		option(f)
	}
	return f
}

// Formatter that generates HTML.
type Formatter struct {
	standalone      bool
	prefix          string
	classes         bool
	tabWidth        int
	lineNumbers     bool
	highlightRanges highlightRanges
}

type highlightRanges [][2]int

func (h highlightRanges) Len() int           { return len(h) }
func (h highlightRanges) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h highlightRanges) Less(i, j int) bool { return h[i][0] < h[j][0] }

func (f *Formatter) Format(w io.Writer, style *chroma.Style) (func(*chroma.Token), error) {
	tokens := []*chroma.Token{}
	return func(token *chroma.Token) {
		tokens = append(tokens, token)
		if token.Type == chroma.EOF {
			f.writeHTML(w, style, tokens)
			return
		}
	}, nil
}

func (f *Formatter) writeHTML(w io.Writer, style *chroma.Style, tokens []*chroma.Token) error {
	// We deliberately don't use html/template here because it is two orders of magnitude slower (benchmarked).
	//
	// OTOH we need to be super careful about correct escaping...
	css := f.styleToCSS(style)
	if !f.classes {
		for t, style := range css {
			css[t] = compressStyle(style)
		}
	}
	if f.standalone {
		fmt.Fprint(w, "<html>\n")
		if f.classes {
			fmt.Fprint(w, "<style type=\"text/css\">\n")
			f.WriteCSS(w, style)
			fmt.Fprintf(w, "body { %s; }\n", css[chroma.Background])
			fmt.Fprint(w, "</style>")
		}
		fmt.Fprintf(w, "<body%s>\n", f.styleAttr(css, chroma.Background))
	}

	fmt.Fprintf(w, "<pre%s>\n", f.styleAttr(css, chroma.Background))
	lines := splitTokensIntoLines(tokens)
	lineDigits := len(fmt.Sprintf("%d", len(lines)))
	highlightIndex := 0
	for line, tokens := range lines {
		highlight := false
		for highlightIndex < len(f.highlightRanges) && line+1 > f.highlightRanges[highlightIndex][1] {
			highlightIndex++
		}
		if highlightIndex < len(f.highlightRanges) {
			hrange := f.highlightRanges[highlightIndex]
			if line+1 >= hrange[0] && line+1 <= hrange[1] {
				highlight = true
			}
		}
		if highlight {
			fmt.Fprintf(w, "<span class=\"hl\">")
		}
		if f.lineNumbers {
			fmt.Fprintf(w, "<span class=\"ln\">%*d</span>", lineDigits, line+1)
		}

		for _, token := range tokens {
			html := html.EscapeString(token.String())
			attr := f.styleAttr(css, token.Type)
			if attr != "" {
				html = fmt.Sprintf("<span%s>%s</span>", attr, html)
			}
			fmt.Fprint(w, html)
		}
		if highlight {
			fmt.Fprintf(w, "</span>")
		}
	}

	fmt.Fprint(w, "</pre>\n")
	if f.standalone {
		fmt.Fprint(w, "</body>\n")
		fmt.Fprint(w, "</html>\n")
	}

	return nil
}

func (f *Formatter) class(tt chroma.TokenType) string {
	switch tt {
	case chroma.Background:
		return "chroma"
	case chroma.LineNumbers:
		return "ln"
	case chroma.LineHighlight:
		return "hl"
	}
	if tt < 0 {
		return fmt.Sprintf("%sss%x", f.prefix, -int(tt))
	}
	return fmt.Sprintf("%ss%x", f.prefix, int(tt))
}

func (f *Formatter) styleAttr(styles map[chroma.TokenType]string, tt chroma.TokenType) string {
	if _, ok := styles[tt]; !ok {
		tt = tt.SubCategory()
		if _, ok := styles[tt]; !ok {
			tt = tt.Category()
			if _, ok := styles[tt]; !ok {
				return ""
			}
		}
	}
	if f.classes {
		return string(fmt.Sprintf(` class="%s"`, f.class(tt)))
	}
	return string(fmt.Sprintf(` style="%s"`, styles[tt]))
}

func (f *Formatter) tabWidthStyle() string {
	if f.tabWidth != 0 && f.tabWidth != 8 {
		return fmt.Sprintf("; -moz-tab-size: %[1]d; -o-tab-size: %[1]d; tab-size: %[1]d", f.tabWidth)
	}
	return ""
}

// WriteCSS writes CSS style definitions (without any surrounding HTML).
func (f *Formatter) WriteCSS(w io.Writer, style *chroma.Style) error {
	css := f.styleToCSS(style)
	// Special-case background as it is mapped to the outer ".chroma" class.
	if _, err := fmt.Fprintf(w, "/* %s */ .chroma { %s }\n", chroma.Background, css[chroma.Background]); err != nil {
		return err
	}
	tts := []int{}
	for tt := range css {
		tts = append(tts, int(tt))
	}
	sort.Ints(tts)
	for _, ti := range tts {
		tt := chroma.TokenType(ti)
		if tt == chroma.Background {
			continue
		}
		styles := css[tt]
		if _, err := fmt.Fprintf(w, "/* %s */ .chroma .%s { %s }\n", tt, f.class(tt), styles); err != nil {
			return err
		}
	}
	return nil
}

func (f *Formatter) styleToCSS(style *chroma.Style) map[chroma.TokenType]string {
	bg := style.Get(chroma.Background)
	classes := map[chroma.TokenType]string{}
	// Convert the style.
	for t := range style.Entries {
		e := style.Entries[t]
		if t != chroma.Background {
			e = e.Sub(bg)
		}
		classes[t] = StyleEntryToCSS(e)
	}
	classes[chroma.Background] += f.tabWidthStyle()
	classes[chroma.LineNumbers] += "; margin-right: 0.5em"
	classes[chroma.LineHighlight] += "; display: block; width: 100%"
	return classes
}

// StyleEntryToCSS converts a chroma.StyleEntry to CSS attributes.
func StyleEntryToCSS(e *chroma.StyleEntry) string {
	styles := []string{}
	if e.Colour.IsSet() {
		styles = append(styles, "color: "+e.Colour.String())
	}
	if e.Background.IsSet() {
		styles = append(styles, "background-color: "+e.Background.String())
	}
	if e.Bold {
		styles = append(styles, "font-weight: bold")
	}
	if e.Italic {
		styles = append(styles, "font-style: italic")
	}
	return strings.Join(styles, "; ")
}

// Compress CSS attributes - remove spaces, transform 6-digit colours to 3.
func compressStyle(s string) string {
	s = strings.Replace(s, " ", "", -1)
	parts := strings.Split(s, ";")
	out := []string{}
	for _, p := range parts {
		if strings.Contains(p, "#") {
			c := p[len(p)-6:]
			if c[0] == c[1] && c[2] == c[3] && c[4] == c[5] {
				p = p[:len(p)-6] + c[0:1] + c[2:3] + c[4:5]
			}
		}
		out = append(out, p)
	}
	return strings.Join(out, ";")
}

func splitTokensIntoLines(tokens []*chroma.Token) (out [][]*chroma.Token) {
	line := []*chroma.Token{}
	for _, token := range tokens {
		for strings.Contains(token.Value, "\n") {
			parts := strings.SplitAfterN(token.Value, "\n", 2)
			// Token becomes the tail.
			token.Value = parts[1]

			// Append the head to the line and flush the line.
			clone := token.Clone()
			clone.Value = parts[0]
			line = append(line, clone)
			out = append(out, line)
			line = nil
		}
		line = append(line, token)
	}
	if len(line) > 0 {
		out = append(out, line)
	}
	return
}
