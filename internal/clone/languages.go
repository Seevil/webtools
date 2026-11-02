package clone

import (
	"net/url"
	"strings"
)

// languageDirectory returns only well-known language path segments in the first
// directory. Ambiguous two-letter paths such as /us/ remain ordinary paths.
func languageDirectory(p string) string {
	p = strings.TrimPrefix(p, "/")
	segment, _, _ := strings.Cut(p, "/")
	segment = strings.ToLower(segment)
	if segment == "zh-hans" || segment == "zh-hant" {
		return segment
	}
	if _, ok := languageDirectories[segment]; ok {
		return segment
	}
	return ""
}

func (c Config) languageAllowed(u, start *url.URL) bool {
	if !c.FilterLanguages {
		return true
	}
	language := languageDirectory(u.Path)
	return language == "" || language == languageDirectory(start.Path)
}

var languageDirectories = map[string]struct{}{
	"ar": {}, "bg": {}, "bn": {}, "cs": {}, "da": {}, "de": {}, "el": {}, "en": {},
	"es": {}, "et": {}, "fa": {}, "fi": {}, "fr": {}, "he": {}, "hi": {}, "hr": {},
	"hu": {}, "id": {}, "it": {}, "ja": {}, "ko": {}, "lt": {}, "lv": {}, "ms": {},
	"nl": {}, "no": {}, "pl": {}, "pt": {}, "ro": {}, "ru": {}, "sk": {}, "sl": {},
	"sr": {}, "sv": {}, "th": {}, "tr": {}, "uk": {}, "ur": {}, "vi": {}, "zh": {},
	"en-us": {}, "en-gb": {}, "en-au": {}, "en-ca": {}, "de-de": {}, "fr-fr": {},
	"fr-ca": {}, "pt-br": {}, "pt-pt": {}, "es-es": {}, "es-mx": {}, "ja-jp": {},
	"ko-kr": {}, "zh-cn": {}, "zh-tw": {}, "zh-hk": {},
}
