package benchmark

import (
	"io"
	"strings"
	"unicode/utf8"

	"github.com/evanw/esbuild/pkg/api"
	"golang.org/x/net/html"
)

// RepairPreviewHTML returns a separate preview only after every inline script
// parses. The saved model output and subsequent continuation input stay intact.
func RepairPreviewHTML(document string) string {
	if len(document) == 0 || len(document) > MaxHTMLBytes || !utf8.ValidString(document) || strings.IndexByte(document, 0) >= 0 {
		return ""
	}
	z := html.NewTokenizer(strings.NewReader(document))
	var output strings.Builder
	output.Grow(len(document))
	inline, scriptCount, templateDepth := false, 0, 0
	repairs, parseBytes := 0, 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() != io.EOF || len(z.Raw()) != 0 || repairs == 0 || output.Len() != len(document) {
				return ""
			}
			return output.String()
		}
		raw := string(z.Raw())
		switch tt {
		case html.SelfClosingTagToken:
			token := z.Token()
			if token.Data == "script" || token.Data == "template" {
				// Their slash has different semantics in HTML and foreign content.
				return ""
			}
		case html.StartTagToken:
			token := z.Token()
			if token.Data == "template" {
				templateDepth++
			}
			if token.Data == "script" {
				inline = templateDepth == 0 && previewJavaScript(token.Attr)
				if inline {
					scriptCount++
					if scriptCount > 64 {
						return ""
					}
				}
			}
		case html.EndTagToken:
			token := z.Token()
			if token.Data == "script" {
				inline = false
			}
			if token.Data == "template" && templateDepth > 0 {
				templateDepth--
			}
		case html.TextToken:
			if inline {
				fixed, ok := repairPreviewScript(raw, &repairs, &parseBytes)
				if !ok {
					return ""
				}
				raw = fixed
			}
		}
		output.WriteString(raw)
	}
}

func previewJavaScript(attrs []html.Attribute) bool {
	scriptType, language := "", ""
	hasType, hasLanguage := false, false
	for _, attr := range attrs {
		switch attr.Key {
		case "src":
			return false
		case "type":
			if !hasType {
				scriptType, hasType = strings.ToLower(strings.TrimSpace(attr.Val)), true
			}
		case "language":
			if !hasLanguage {
				language, hasLanguage = strings.ToLower(strings.TrimSpace(attr.Val)), true
			}
		}
	}
	if !hasType && language != "" {
		scriptType = "text/" + language
	}
	switch scriptType {
	case "", "module", "text/javascript", "application/javascript", "text/ecmascript", "application/ecmascript":
		return true
	default:
		return false
	}
}

func repairPreviewScript(script string, repairs, parseBytes *int) (string, bool) {
	for {
		*parseBytes += len(script)
		if *parseBytes > 4*MaxHTMLBytes {
			return "", false
		}
		result := api.Transform(script, api.TransformOptions{Loader: api.LoaderJS, Target: api.ESNext, LogLevel: api.LogLevelSilent})
		if len(result.Errors) == 0 {
			// Never use the transform's output: formatting, comments and behavior
			// must stay byte-for-byte unchanged apart from verified punctuation.
			return script, true
		}
		for _, problem := range result.Errors {
			// esbuild defers invalid shorthand initializers until it can rule out
			// destructuring. Generic "expected colon" errors are ambiguous.
			if problem.Text != `Unexpected "="` {
				return "", false
			}
			at := previewErrorOffset(script, problem.Location)
			if at < 0 || script[at] != '=' || *repairs >= 16 {
				return "", false
			}
			script = script[:at] + ":" + script[at+1:]
			*repairs++
		}
	}
}

func previewErrorOffset(script string, loc *api.Location) int {
	if loc == nil || loc.Line < 1 || loc.Column < 0 || loc.Length != 1 {
		return -1
	}
	line, start := 1, 0
	for at := 0; at < len(script) && line < loc.Line; {
		r, size := utf8.DecodeRuneInString(script[at:])
		at += size
		if r == '\r' && at < len(script) && script[at] == '\n' {
			at++
		}
		if r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029' {
			line++
			start = at
		}
	}
	at := start + loc.Column
	if line != loc.Line || at >= len(script) {
		return -1
	}
	return at
}
