package fonts

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The system font scan is shared by all libraries of the process, like
// fontconfig's cache: rendering several documents only pays for it once.
// A cached scan is reused while none of the scanned directories changed
// (adding or removing a font file changes its directory's mtime).

type scanResult struct {
	faces []FaceInfo           // templates, copied into each library
	dirs  map[string]time.Time // every directory walked, with its mtime
}

var scanCache = struct {
	sync.Mutex
	byKey    map[string]*scanResult
	coverage map[string][]runeRange // path + index → cmap coverage
}{byKey: map[string]*scanResult{}, coverage: map[string][]runeRange{}}

func (s *scanResult) fresh() bool {
	for dir, mt := range s.dirs {
		st, err := os.Stat(dir)
		if err != nil || !st.ModTime().Equal(mt) {
			return false
		}
	}
	return true
}

// cachedScan returns copies of the faces found in dirs, scanning them if
// needed.
func cachedScan(dirs []string, index func(path string) []*FaceInfo) []*FaceInfo {
	key := strings.Join(dirs, "\x00")
	scanCache.Lock()
	res := scanCache.byKey[key]
	scanCache.Unlock()
	if res == nil || !res.fresh() {
		res = &scanResult{dirs: map[string]time.Time{}}
		seen := map[string]bool{}
		for _, dir := range dirs {
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				res.dirs[dir] = st.ModTime()
			} else {
				// Watch the parent so that creating the directory
				// invalidates the scan.
				if st, err := os.Stat(filepath.Dir(dir)); err == nil {
					res.dirs[filepath.Dir(dir)] = st.ModTime()
				}
				continue
			}
			filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					if info, err := d.Info(); err == nil {
						res.dirs[path] = info.ModTime()
					}
					return nil
				}
				real, err := filepath.EvalSymlinks(path)
				if err != nil || seen[real] {
					return nil
				}
				seen[real] = true
				switch strings.ToLower(filepath.Ext(path)) {
				case ".ttf", ".otf", ".ttc", ".otc", ".woff":
					for _, fi := range index(real) {
						res.faces = append(res.faces, *fi)
					}
				}
				return nil
			})
		}
		scanCache.Lock()
		scanCache.byKey[key] = res
		scanCache.coverage = map[string][]runeRange{} // files may have changed
		scanCache.Unlock()
	}
	out := make([]*FaceInfo, len(res.faces))
	for i := range res.faces {
		fi := res.faces[i]
		out[i] = &fi
	}
	return out
}

func coverageKey(fi *FaceInfo) string {
	return fi.Path + "\x00" + strconv.Itoa(fi.Index)
}

// loadCoverage reads the cmap coverage of a system face, sharing it
// between libraries.
func loadCoverage(fi *FaceInfo) []runeRange {
	k := coverageKey(fi)
	scanCache.Lock()
	cov, ok := scanCache.coverage[k]
	scanCache.Unlock()
	if ok {
		return cov
	}
	if fi.cmapLen > 0 && fi.cmapLen < 16<<20 {
		if fh, err := os.Open(fi.Path); err == nil {
			data := make([]byte, fi.cmapLen)
			if n, _ := fh.ReadAt(data, int64(fi.cmapOff)); n == fi.cmapLen {
				cov = cmapRanges(data)
			}
			fh.Close()
		}
	}
	scanCache.Lock()
	scanCache.coverage[k] = cov
	scanCache.Unlock()
	return cov
}

// Parsed system fonts, shared by all libraries. Fonts are read-only once
// parsed (lazy tables are synchronized), so renders can share them.
var fontCache = struct {
	sync.Mutex
	m map[string]*cachedFont
}{m: map[string]*cachedFont{}}

type cachedFont struct {
	once    sync.Once
	mod     time.Time
	size    int64
	font    *Font
	err     error
	missing bool
}

// loadShared returns the parsed face idx of the font file at path. It
// returns nil, nil when the file can't be read.
func loadShared(path string, idx int) (*Font, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, nil
	}
	key := path + "\x00" + strconv.Itoa(idx)
	fontCache.Lock()
	c := fontCache.m[key]
	if c == nil || !c.mod.Equal(st.ModTime()) || c.size != st.Size() {
		c = &cachedFont{mod: st.ModTime(), size: st.Size()}
		fontCache.m[key] = c
	}
	fontCache.Unlock()
	c.once.Do(func() {
		data, err := os.ReadFile(path)
		if err != nil {
			c.missing = true
			return
		}
		c.font, c.err = Parse(data, idx)
	})
	if c.missing {
		return nil, nil
	}
	return c.font, c.err
}
