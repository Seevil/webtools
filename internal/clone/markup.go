package clone

import (
	"html"
	"net/url"
	"strings"
)

type attribute struct {
	name, value string
	start, end  int
	quote       byte
}
type tag struct {
	name       string
	start, end int
	attrs      []attribute
	closing    bool
}
type replacement struct {
	start, end int
	text       string
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func space(c byte) bool    { return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' }
func nameChar(c byte) bool { return c > 32 && !strings.ContainsRune("/=>\"'", rune(c)) }

// Offsets preserve original encoding and script bodies rather than serializing a DOM.
func tags(s string) []tag {
	var out []tag
	lower := asciiLower(s)
	for i := 0; i < len(s); {
		p := strings.IndexByte(s[i:], '<')
		if p < 0 {
			break
		}
		i += p
		if strings.HasPrefix(s[i:], "<!--") {
			e := strings.Index(s[i+4:], "-->")
			if e < 0 {
				break
			}
			i += e + 7
			continue
		}
		start := i
		i++
		closing := i < len(s) && s[i] == '/'
		if closing {
			i++
		}
		a := i
		for i < len(s) && nameChar(s[i]) {
			i++
		}
		if a == i {
			continue
		}
		t := tag{name: strings.ToLower(s[a:i]), start: start, closing: closing}
		for i < len(s) && s[i] != '>' {
			for i < len(s) && (space(s[i]) || s[i] == '/') {
				i++
			}
			if i >= len(s) || s[i] == '>' {
				break
			}
			n := i
			for i < len(s) && nameChar(s[i]) {
				i++
			}
			if n == i {
				i++
				continue
			}
			at := attribute{name: strings.ToLower(s[n:i])}
			for i < len(s) && space(s[i]) {
				i++
			}
			if i < len(s) && s[i] == '=' {
				i++
				for i < len(s) && space(s[i]) {
					i++
				}
				if i < len(s) && (s[i] == '\'' || s[i] == '"') {
					at.quote = s[i]
					i++
					at.start = i
					for i < len(s) && s[i] != at.quote {
						i++
					}
					at.end = i
					if i < len(s) {
						i++
					}
				} else {
					at.start = i
					for i < len(s) && !space(s[i]) && s[i] != '>' {
						i++
					}
					at.end = i
				}
				at.value = html.UnescapeString(s[at.start:at.end])
			}
			t.attrs = append(t.attrs, at)
		}
		if i < len(s) {
			i++
		}
		t.end = i
		out = append(out, t)
		if !closing && (t.name == "script" || t.name == "style" || t.name == "textarea" || t.name == "title") {
			if e := strings.Index(lower[i:], "</"+t.name); e >= 0 {
				i += e
			} else {
				break
			}
		}
	}
	return out
}

func attr(t tag, name string) string {
	for _, a := range t.attrs {
		if a.name == name {
			return a.value
		}
	}
	return ""
}

func assetAttribute(t tag, a attribute) (bool, bool) {
	switch a.name {
	case "src":
		return t.name != "iframe" && t.name != "frame", t.name == "iframe" || t.name == "frame"
	case "poster", "background":
		return true, false
	case "data":
		return t.name == "object", false
	case "href":
		if t.name == "a" || t.name == "area" {
			return false, true
		}
		if t.name == "link" {
			r := " " + strings.ToLower(attr(t, "rel")) + " "
			for _, rel := range []string{"stylesheet", "icon", "preload", "modulepreload", "apple-touch-icon"} {
				if strings.Contains(r, " "+rel+" ") {
					return true, false
				}
			}
		}
	}
	return false, false
}

func documentBase(s string, origin *url.URL) *url.URL {
	for _, t := range tags(s) {
		if t.name == "base" && !t.closing {
			if u := resolve(origin, attr(t, "href")); u != nil {
				return u
			}
		}
	}
	return origin
}

func resolve(base *url.URL, raw string) *url.URL {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	u = base.ResolveReference(u)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil
	}
	return canonical(u)
}

func apply(s string, edits []replacement) string {
	var b strings.Builder
	last := 0
	for _, r := range edits {
		if r.start < last || r.end > len(s) {
			continue
		}
		b.WriteString(s[last:r.start])
		b.WriteString(r.text)
		last = r.end
	}
	b.WriteString(s[last:])
	return b.String()
}

func srcset(s string, rewrite func(string) string) string {
	// Data URLs contain commas. Preserve that whole attribute rather than corrupting it.
	if strings.Contains(strings.ToLower(s), "data:") {
		return s
	}
	parts := strings.Split(s, ",")
	for i, p := range parts {
		v := strings.Fields(p)
		if len(v) > 0 {
			v[0] = rewrite(v[0])
			parts[i] = strings.Join(v, " ")
		}
	}
	return strings.Join(parts, ", ")
}

func processHTML(s string, rewrite func(string, bool) string) string {
	ts := tags(s)
	var edits []replacement
	for j, t := range ts {
		if t.closing {
			continue
		}
		if t.name == "base" {
			edits = append(edits, replacement{t.start, t.end, ""})
			continue
		}
		if t.name == "meta" {
			h := strings.ToLower(attr(t, "http-equiv"))
			if h == "content-security-policy" || h == "refresh" {
				edits = append(edits, replacement{t.start, t.end, ""})
				continue
			}
		}
		for _, a := range t.attrs {
			if a.end <= a.start {
				continue
			}
			value := a.value
			asset, page := assetAttribute(t, a)
			switch {
			case asset || page:
				value = rewrite(value, page)
			case a.name == "srcset":
				value = srcset(value, func(v string) string { return rewrite(v, false) })
			case a.name == "style":
				value = processCSS(value, func(v string) string { return rewrite(v, false) })
			case a.name == "action" || a.name == "formaction":
				value = "#"
			case a.name == "integrity" && t.name == "link" && strings.Contains(strings.ToLower(attr(t, "rel")), "stylesheet"):
				value = ""
			}
			if value != a.value {
				value = html.EscapeString(value)
				if a.quote == 0 {
					value = "\"" + value + "\""
				}
				edits = append(edits, replacement{a.start, a.end, value})
			}
		}
		if t.name == "style" && j+1 < len(ts) && ts[j+1].name == "style" && ts[j+1].closing {
			end := ts[j+1].start
			edits = append(edits, replacement{t.end, end, processCSS(s[t.end:end], func(v string) string { return rewrite(v, false) })})
		}
	}
	return apply(s, edits)
}

func quotedEnd(s string, i int, q byte) int {
	for i < len(s) {
		if s[i] == '\\' {
			i += 2
			continue
		}
		if s[i] == q {
			return i
		}
		i++
	}
	return len(s)
}

func processCSS(s string, rewrite func(string) string) string {
	lower := asciiLower(s)
	var edits []replacement
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "/*") {
			e := strings.Index(s[i+2:], "*/")
			if e < 0 {
				break
			}
			i += e + 4
			continue
		}
		start, end, next := -1, -1, i+1
		if strings.HasPrefix(lower[i:], "url(") {
			k := i + 4
			for k < len(s) && space(s[k]) {
				k++
			}
			if k < len(s) && (s[k] == '\'' || s[k] == '"') {
				start = k + 1
				end = quotedEnd(s, start, s[k])
				next = end + 1
			} else {
				start = k
				for k < len(s) && s[k] != ')' {
					k++
				}
				end = k
				for end > start && space(s[end-1]) {
					end--
				}
				next = k + 1
			}
		} else if strings.HasPrefix(lower[i:], "@import") {
			k := i + 7
			for k < len(s) && space(s[k]) {
				k++
			}
			if k < len(s) && (s[k] == '\'' || s[k] == '"') {
				start = k + 1
				end = quotedEnd(s, start, s[k])
				next = end + 1
			}
		} else if s[i] == '\'' || s[i] == '"' {
			next = quotedEnd(s, i+1, s[i]) + 1
		}
		if start >= 0 && end >= start && end <= len(s) {
			v := s[start:end]
			// Escaped CSS identifiers require a full CSS parser; keep them intact.
			if !strings.ContainsRune(v, '\\') {
				edits = append(edits, replacement{start, end, rewrite(v)})
			}
		}
		i = next
	}
	return apply(s, edits)
}
