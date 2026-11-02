package clone

import (
	"mime"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"
)

var (
	metaCharsetRE = regexp.MustCompile(`(?i)<meta\b[^>]*?charset\s*=\s*["']?\s*([a-zA-Z0-9_\-:.]+)[^>]*>`)
	cssCharsetRE  = regexp.MustCompile(`(?i)^\s*@charset\s+["']([^"']+)["']\s*;`)
	headOpenRE    = regexp.MustCompile(`(?i)<head\b[^>]*>`)
	doctypeRE     = regexp.MustCompile(`(?i)^\s*<!doctype[^>]*>`)
)

// detectCharset returns the declared charset from the Content-Type header, then from the document itself.
func detectCharset(contentType string, body []byte, isHTML bool) string {
	if _, params, err := mime.ParseMediaType(contentType); err == nil && params["charset"] != "" {
		return strings.ToLower(strings.TrimSpace(params["charset"]))
	}
	head := body
	if len(head) > 4096 {
		head = head[:4096]
	}
	if isHTML {
		if m := metaCharsetRE.FindSubmatch(head); m != nil {
			return strings.ToLower(string(m[1]))
		}
	} else if m := cssCharsetRE.FindSubmatch(head); m != nil {
		return strings.ToLower(string(m[1]))
	}
	return ""
}

func isUTF8Name(name string) bool {
	n := strings.ReplaceAll(strings.ToLower(name), "_", "-")
	return n == "utf-8" || n == "utf8"
}

// toUTF8 converts HTML or CSS text into UTF-8 and updates its charset declaration.
// It returns the original bytes and false when the source charset is unknown or conversion fails.
func toUTF8(body []byte, contentType string, isHTML bool) ([]byte, bool) {
	charset := detectCharset(contentType, body, isHTML)
	if charset == "" || isUTF8Name(charset) {
		if !utf8.Valid(body) {
			return body, false
		}
		text := string(body)
		if isHTML {
			text = declareHTMLUTF8(text)
		} else {
			text = declareCSSUTF8(text)
		}
		return []byte(text), text != string(body)
	}
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return body, false
	}
	decoded, err := enc.NewDecoder().Bytes(body)
	if err != nil {
		return body, false
	}
	text := string(decoded)
	if isHTML {
		text = declareHTMLUTF8(text)
	} else {
		text = declareCSSUTF8(text)
	}
	return []byte(text), true
}

func declareHTMLUTF8(text string) string {
	if loc := metaCharsetRE.FindStringSubmatchIndex(text); loc != nil {
		return text[:loc[2]] + "utf-8" + text[loc[3]:]
	}
	meta := `<meta charset="utf-8">`
	if loc := headOpenRE.FindStringIndex(text); loc != nil {
		return text[:loc[1]] + meta + text[loc[1]:]
	}
	if loc := doctypeRE.FindStringIndex(text); loc != nil {
		return text[:loc[1]] + meta + text[loc[1]:]
	}
	return meta + text
}

func declareCSSUTF8(text string) string {
	if loc := cssCharsetRE.FindStringSubmatchIndex(text); loc != nil {
		return text[:loc[2]] + "utf-8" + text[loc[3]:]
	}
	return text
}
