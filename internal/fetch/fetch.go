// Package fetch implements URL resolution and the default URL fetcher,
// supporting file:, data:, http: and https: URLs.
package fetch

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Result is the result of fetching a URL.
type Result struct {
	Data        []byte
	MimeType    string
	Encoding    string // charset, if known
	RedirectURL string // final URL after redirects
}

// Fetcher fetches the resource at the given absolute URL.
type Fetcher func(url string) (*Result, error)

// UserAgent is sent with HTTP requests.
const UserAgent = "WeasyPrint-Go (https://github.com/esoteric-programmer/weasyprint-go)"

// Timeout for HTTP requests.
var Timeout = 10 * time.Second

// Default is the default URL fetcher.
func Default(rawURL string) (*Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "data":
		return fetchData(rawURL)
	case "file", "":
		path := u.Path
		if u.Scheme == "" {
			path = rawURL
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
		mt, params, _ := mime.ParseMediaType(mt)
		return &Result{Data: data, MimeType: mt, Encoding: params["charset"], RedirectURL: rawURL}, nil
	case "http", "https":
		client := &http.Client{Timeout: Timeout}
		req, err := http.NewRequest("GET", rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", UserAgent)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, rawURL)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		mt, params, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		return &Result{Data: data, MimeType: mt, Encoding: params["charset"], RedirectURL: resp.Request.URL.String()}, nil
	}
	return nil, fmt.Errorf("unsupported URL scheme %q", u.Scheme)
}

func fetchData(raw string) (*Result, error) {
	rest := strings.TrimPrefix(raw, "data:")
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return nil, errors.New("invalid data: URL")
	}
	meta, payload := rest[:comma], rest[comma+1:]
	isBase64 := false
	if strings.HasSuffix(strings.ToLower(meta), ";base64") {
		isBase64 = true
		meta = meta[:len(meta)-7]
	}
	if meta == "" {
		meta = "text/plain;charset=US-ASCII"
	}
	mt, params, _ := mime.ParseMediaType(meta)
	var data []byte
	if isBase64 {
		p, _ := url.PathUnescape(payload)
		p = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
				return -1
			}
			return r
		}, p)
		var err error
		data, err = base64.StdEncoding.DecodeString(p)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(p, "="))
			if err != nil {
				return nil, err
			}
		}
	} else {
		p, err := url.PathUnescape(payload)
		if err != nil {
			p = payload
		}
		data = []byte(p)
	}
	return &Result{Data: data, MimeType: mt, Encoding: params["charset"], RedirectURL: raw}, nil
}

// Resolve resolves a possibly relative URL against a base URL.
func Resolve(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return base
	}
	if strings.HasPrefix(ref, "data:") {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if r.IsAbs() || base == "" {
		return ref
	}
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

// PathToURL converts a filesystem path into a file:// URL.
func PathToURL(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if fi, err := os.Stat(abs); err == nil && fi.IsDir() && !strings.HasSuffix(abs, "/") {
		abs += "/"
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return u.String()
}

// EnsureURL turns a string that may be a path into a URL.
func EnsureURL(s string) string {
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && len(u.Scheme) > 1 {
		return s
	}
	return PathToURL(s)
}
